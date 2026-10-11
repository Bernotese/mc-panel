package files

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	// maxCentralDirBytes bounds the central directory archive/zip reads into memory (names,
	// extra fields and comments of every entry).
	maxCentralDirBytes = 64 << 20

	cdHeaderSig    = 0x02014b50
	cdHeaderLen    = 46
	eocdSig        = 0x06054b50
	eocdLen        = 22
	zip64LocSig    = 0x07064b50
	zip64LocLen    = 20
	zip64EOCDSig   = 0x06064b50
	zip64EOCDLen   = 56
	eocdSearchSize = 65 * 1024 // archive/zip looks in the last 1 KiB, then the last 65 KiB
)

// checkCentralDirectory bounds what zip.NewReader would allocate, before it runs: archive/zip
// reads every central-directory header into a *zip.File (and may preallocate by the declared
// count) before any limit of ours applies. This walks the directory the same way, but only
// counts: more than maxEntries headers (declared or present) or more than maxCentralDirBytes
// of directory are ErrTooLarge. It reads a few KiB at a time; anything it cannot make sense of
// is left to zip.NewReader, which then fails with ErrInvalid.
func checkCentralDirectory(r io.ReaderAt, size, maxEntries int64) error {
	end, endOff, ok := findDirectoryEnd(r, size)
	if !ok {
		return nil // not a zip: zip.NewReader reports it
	}
	records := uint64(binary.LittleEndian.Uint16(end[10:]))
	cdSize := uint64(binary.LittleEndian.Uint32(end[12:]))
	cdOff := uint64(binary.LittleEndian.Uint32(end[16:]))
	if records == 0xffff || cdSize == 0xffffffff || cdOff == 0xffffffff {
		if r64, s64, o64, off, ok := readZip64End(r, endOff); ok {
			records, cdSize, cdOff, endOff = r64, s64, o64, off
		}
	}
	if records > uint64(maxEntries) {
		return fmt.Errorf("%w: the archive declares more than %d entries", filesapi.ErrTooLarge, maxEntries)
	}
	if cdSize > math.MaxInt64 || cdOff > math.MaxInt64 {
		return nil
	}
	// archive/zip starts at base+cdOff, or at cdOff when a header sits there: count both.
	base := endOff - int64(cdSize) - int64(cdOff)
	for _, start := range []int64{base + int64(cdOff), int64(cdOff)} {
		if start < 0 || start >= size {
			continue
		}
		if err := countHeaders(io.NewSectionReader(r, start, size-start), maxEntries); err != nil {
			return err
		}
	}
	return nil
}

// findDirectoryEnd locates the end-of-central-directory record like archive/zip.
func findDirectoryEnd(r io.ReaderAt, size int64) ([]byte, int64, bool) {
	for _, n := range []int64{1024, eocdSearchSize} {
		n = min(n, size)
		buf := make([]byte, n)
		if _, err := r.ReadAt(buf, size-n); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, false
		}
		for i := len(buf) - eocdLen; i >= 0; i-- {
			if binary.LittleEndian.Uint32(buf[i:]) != eocdSig {
				continue
			}
			if c := int(binary.LittleEndian.Uint16(buf[i+eocdLen-2:])); i+eocdLen+c > len(buf) {
				return nil, 0, false // truncated comment: archive/zip gives up as well
			}
			return buf[i : i+eocdLen], size - n + int64(i), true
		}
		if n == size {
			break
		}
	}
	return nil, 0, false
}

// readZip64End reads the zip64 end record through its locator, like archive/zip.
func readZip64End(r io.ReaderAt, endOff int64) (records, cdSize, cdOff uint64, off int64, ok bool) {
	if endOff < zip64LocLen {
		return 0, 0, 0, 0, false
	}
	loc := make([]byte, zip64LocLen)
	if _, err := r.ReadAt(loc, endOff-zip64LocLen); err != nil {
		return 0, 0, 0, 0, false
	}
	if binary.LittleEndian.Uint32(loc) != zip64LocSig || binary.LittleEndian.Uint32(loc[4:]) != 0 ||
		binary.LittleEndian.Uint32(loc[16:]) != 1 {
		return 0, 0, 0, 0, false
	}
	p := binary.LittleEndian.Uint64(loc[8:])
	if p > math.MaxInt64 {
		return 0, 0, 0, 0, false
	}
	b := make([]byte, zip64EOCDLen)
	if _, err := r.ReadAt(b, int64(p)); err != nil || binary.LittleEndian.Uint32(b) != zip64EOCDSig {
		return 0, 0, 0, 0, false
	}
	return binary.LittleEndian.Uint64(b[32:]), binary.LittleEndian.Uint64(b[40:]), binary.LittleEndian.Uint64(b[48:]), int64(p), true
}

// countHeaders counts consecutive central-directory headers (fixed part plus name, extra and
// comment) until something else follows, as archive/zip reads them.
func countHeaders(r io.Reader, maxEntries int64) error {
	br := bufio.NewReader(r)
	var n, total int64
	for {
		size, ok := nextHeader(br)
		if !ok {
			return nil
		}
		n++
		total += size
		switch {
		case n > maxEntries:
			return fmt.Errorf("%w: the archive has more than %d entries", filesapi.ErrTooLarge, maxEntries)
		case total > maxCentralDirBytes:
			return fmt.Errorf("%w: the archive's central directory exceeds %d bytes", filesapi.ErrTooLarge, maxCentralDirBytes)
		}
	}
}

// nextHeader skips one complete central-directory header and returns its size; ok is false
// when no complete header follows (where archive/zip stops reading as well).
func nextHeader(br *bufio.Reader) (size int64, ok bool) {
	var hdr [cdHeaderLen]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil || binary.LittleEndian.Uint32(hdr[:]) != cdHeaderSig {
		return 0, false
	}
	vl := int(binary.LittleEndian.Uint16(hdr[28:])) + int(binary.LittleEndian.Uint16(hdr[30:])) + int(binary.LittleEndian.Uint16(hdr[32:]))
	if _, err := br.Discard(vl); err != nil {
		return 0, false
	}
	return int64(cdHeaderLen + vl), true
}
