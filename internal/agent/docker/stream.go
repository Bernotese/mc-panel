package docker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxFrameBytes bounds one frame of Docker's multiplexed stream format; a larger length
// in a header means a broken (or hostile) stream.
const MaxFrameBytes = 1 << 20

// StreamType is the first header byte of a multiplexed frame.
type StreamType byte

const (
	Stdin  StreamType = 0
	Stdout StreamType = 1
	Stderr StreamType = 2
)

// Frame is one frame of a multiplexed stream.
type Frame struct {
	Stream StreamType
	Data   []byte
}

// ErrBadFrame reports a frame header that violates the format.
var ErrBadFrame = errors.New("docker: bad multiplexed stream frame")

// Demuxer reads Docker's multiplexed stream format, used for logs and exec output of
// containers without TTY: an 8-byte header (stream type, three zero bytes, payload
// length as big-endian uint32) followed by the payload.
type Demuxer struct {
	r   io.Reader
	hdr [8]byte
}

func NewDemuxer(r io.Reader) *Demuxer { return &Demuxer{r: r} }

// Next returns the next frame: io.EOF at a clean end between frames,
// io.ErrUnexpectedEOF inside a frame, ErrBadFrame for an invalid header.
func (d *Demuxer) Next() (Frame, error) {
	if _, err := io.ReadFull(d.r, d.hdr[:]); err != nil {
		return Frame{}, err // io.EOF if nothing was read, io.ErrUnexpectedEOF if cut
	}
	st := StreamType(d.hdr[0])
	if st > Stderr || d.hdr[1] != 0 || d.hdr[2] != 0 || d.hdr[3] != 0 {
		return Frame{}, fmt.Errorf("%w: header % x", ErrBadFrame, d.hdr)
	}
	n := binary.BigEndian.Uint32(d.hdr[4:])
	if n > MaxFrameBytes {
		return Frame{}, fmt.Errorf("%w: frame of %d bytes exceeds %d", ErrBadFrame, n, MaxFrameBytes)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(d.r, data); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return Frame{Stream: st, Data: data}, nil
}

// WriteFrame writes data as one frame. The agent only reads frames; the fake daemon
// and tests write them.
func WriteFrame(w io.Writer, st StreamType, data []byte) error {
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("%w: frame of %d bytes exceeds %d", ErrBadFrame, len(data), MaxFrameBytes)
	}
	var hdr [8]byte
	hdr[0] = byte(st)
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}
