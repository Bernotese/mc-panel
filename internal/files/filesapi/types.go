package filesapi

import (
	"context"
	"io"
)

const (
	PathHealth   = "/v1/health"
	PathList     = "/v1/list"
	PathRead     = "/v1/read"
	PathWrite    = "/v1/write"
	PathUpload   = "/v1/upload"   // PUT, query id, path, overwrite=0|1; body = file; Content-Length required
	PathDownload = "/v1/download" // GET, query id, path
	PathMkdir    = "/v1/mkdir"
	PathRename   = "/v1/rename"
	PathDelete   = "/v1/delete"
	PathExtract  = "/v1/extract"
	PathJobs     = "/v1/jobs" // GET, query id

	// HeaderKind is set on download answers: "file" or "dir" (a zip stream).
	HeaderKind = "X-Files-Kind"

	// MaxJSONBytes bounds a write request: a 5 MiB text can grow up to 6x by JSON escaping
	// (control characters become \u00XX), so the limit must exceed 6 × max_text_bytes.
	MaxJSONBytes      = 32 << 20
	MaxSmallJSONBytes = 64 << 10
)

type EntryType string

const (
	TypeFile    EntryType = "file"
	TypeDir     EntryType = "dir"
	TypeSymlink EntryType = "symlink"
	TypeOther   EntryType = "other"
)

type Entry struct {
	Name  string    `json:"name"`
	Type  EntryType `json:"type"`
	Size  int64     `json:"size"`
	MTime int64     `json:"mtime"` // Unix ms
}

// PathRequest is the body of list, read, mkdir and delete.
type PathRequest struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

type ListResponse struct {
	Entries   []Entry `json:"entries"` // never nil
	Truncated bool    `json:"truncated"`
}

type ReadResponse struct {
	Content string `json:"content"`
	MTime   int64  `json:"mtime"`
	Size    int64  `json:"size"`
}

type WriteRequest struct {
	ID            int64  `json:"id"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	ExpectedMTime *int64 `json:"expected_mtime"` // nil = must not exist yet
}

type WriteResponse struct {
	MTime int64 `json:"mtime"`
	Size  int64 `json:"size"`
}

type RenameRequest struct {
	ID   int64  `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

type UploadResponse struct {
	Size int64 `json:"size"`
}

type ExtractRequest struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Dest string `json:"dest"`
}

type JobState string

const (
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

type Job struct {
	ID           string   `json:"id"`
	State        JobState `json:"state"`
	Path         string   `json:"path"`
	Dest         string   `json:"dest"`
	Bytes        int64    `json:"bytes"`
	Entries      int64    `json:"entries"`
	TotalBytes   int64    `json:"total_bytes"`
	TotalEntries int64    `json:"total_entries"`
	Error        string   `json:"error"`
	StartedAt    int64    `json:"started_at"`  // Unix ms
	FinishedAt   int64    `json:"finished_at"` // 0 while running
}

type JobsResponse struct {
	Job *Job `json:"job"`
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Download is an open download; the caller must Close Body.
type Download struct {
	Body  io.ReadCloser
	Size  int64  // -1 for a directory zip
	IsDir bool   // HeaderKind == "dir"
	Name  string // base name of the path ("" for the root: the caller uses the server name)
}

// Client is the panel's view of the service (implemented by *UnixClient).
type Client interface {
	Health(ctx context.Context) error
	List(ctx context.Context, id int64, path string) (ListResponse, error)
	Read(ctx context.Context, id int64, path string) (ReadResponse, error)
	Write(ctx context.Context, req WriteRequest) (WriteResponse, error)
	Upload(ctx context.Context, id int64, path string, overwrite bool, size int64, body io.Reader) (UploadResponse, error)
	Download(ctx context.Context, id int64, path string) (Download, error)
	Mkdir(ctx context.Context, id int64, path string) error
	Rename(ctx context.Context, req RenameRequest) error
	Delete(ctx context.Context, id int64, path string) error
	Extract(ctx context.Context, req ExtractRequest) (Job, error)
	Job(ctx context.Context, id int64) (*Job, error)
}
