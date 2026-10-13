package files

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	// backupEndName is the last entry of every backup stream: a regular file with the number
	// of entries and content bytes before it. restic turns a stdin that ends early (the
	// service died, a pipe broke) into a snapshot all the same; restore and download accept a
	// snapshot only with this entry at its end and matching counts. The name is hidden
	// (filesapi.IsHidden), so the walk of a data directory can never produce it.
	backupEndName     = ".mcpanel-end"
	maxBackupEndBytes = 4 << 10
)

// maxBackupNameBytes bounds the sum of all path bytes of one backup stream, when it is written
// (names read from the data directory, also the ones left out) and when it is read back
// (restore, download). Both sides count the path without a trailing "/", and the reader sees
// no more names than the writer read, so every snapshot the writer accepted can be restored.
// A download is further bounded by the zip's own maxZipNameBytes (16 MiB, the central
// directory is held in memory): a snapshot with more names restores, but its zip download
// fails with too_large. A variable so tests can lower it.
var maxBackupNameBytes int64 = 64 << 20

// errIncomplete: the end entry is missing, damaged or does not match the stream before it.
var errIncomplete = fmt.Errorf("%w: the snapshot is incomplete", filesapi.ErrInvalid)

// backupEnd is the content of the end entry.
type backupEnd struct {
	Entries int64 `json:"entries"` // folders and regular files before the end entry
	Bytes   int64 `json:"bytes"`   // content bytes of those files
}

// writeBackupEnd appends the end entry.
func writeBackupEnd(tw *tar.Writer, e backupEnd) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: backupEndName, Size: int64(len(b)), Mode: 0o600,
		ModTime: time.Unix(0, 0), Format: tar.FormatPAX,
	}); err != nil {
		return err
	}
	_, err = tw.Write(b)
	return err
}

// checkBackupEnd reads the end entry hdr from tr, compares it with what the reader counted and
// requires the stream to end right after it. Every failure is ErrInvalid.
func checkBackupEnd(tr *tar.Reader, hdr *tar.Header, got backupEnd) error {
	if hdr.Typeflag != tar.TypeReg || hdr.Size <= 0 || hdr.Size > maxBackupEndBytes {
		return errIncomplete
	}
	b, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
	if err != nil || int64(len(b)) != hdr.Size {
		return errIncomplete
	}
	var want backupEnd
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&want); err != nil || want != got {
		return errIncomplete
	}
	switch _, err := tr.Next(); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return fmt.Errorf("%w: the snapshot has entries after its end", filesapi.ErrInvalid)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return errIncomplete
	default:
		return fmt.Errorf("%w: the backup stream is damaged: %w", filesapi.ErrInvalid, err)
	}
}
