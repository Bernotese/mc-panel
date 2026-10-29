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
	PathUpload   = "/v1/upload"   // PUT, query id, path, overwrite=0|1 and at most one of sha256/sha512; body = file; Content-Length required
	PathDownload = "/v1/download" // GET, query id, path
	PathMkdir    = "/v1/mkdir"
	PathRename   = "/v1/rename"
	PathDelete   = "/v1/delete"
	PathExtract  = "/v1/extract"
	PathJobs     = "/v1/jobs" // GET, query id
	PathJars     = "/v1/jars" // POST JarsRequest -> JarsResponse (sub-project 3c)

	// HeaderKind is set on download answers: "file" or "dir" (a zip stream).
	HeaderKind = "X-Files-Kind"

	// MaxJSONBytes bounds a write request: a 5 MiB text can grow up to 6x by JSON escaping
	// (control characters become \u00XX), so the limit must exceed 6 × max_text_bytes.
	MaxJSONBytes      = 32 << 20
	MaxSmallJSONBytes = 64 << 10
)

// Plugins and mods (sub-project 3c §5).
const (
	// DirPlugins and DirMods are the only directories /v1/jars lists.
	DirPlugins = "plugins"
	DirMods    = "mods"
	// MaxJars bounds one listing (JarsResponse.Truncated beyond it).
	MaxJars = 500
	// MaxJarHashBytes: larger jars are listed without checksums.
	MaxJarHashBytes = 256 << 20

	// AlgoSHA256 and AlgoSHA512 are the checksum algorithms of an upload.
	AlgoSHA256 = "sha256"
	AlgoSHA512 = "sha512"
	// QuerySHA256 and QuerySHA512 are the upload query parameters carrying the expected digest
	// (lowercase hex); at most one of them per upload.
	QuerySHA256 = AlgoSHA256
	QuerySHA512 = AlgoSHA512

	// JarSuffix and DisabledSuffix make up jar names: "x.jar" is enabled, "x.jar.disabled" not.
	JarSuffix      = ".jar"
	DisabledSuffix = ".disabled"
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
	// Version identifies this state of the file (opaque; device, inode, size, mtime and ctime
	// in ns). Every save through the service changes it; so does almost any other write.
	Version string `json:"version"`
}

// WriteRequest saves editor content. With neither ExpectedMTime nor ExpectedVersion the file
// must not exist yet; each one that is given must match the current file (ErrModified). Both
// are checked again right before the new file replaces the old one.
type WriteRequest struct {
	ID              int64   `json:"id"`
	Path            string  `json:"path"`
	Content         string  `json:"content"`
	ExpectedMTime   *int64  `json:"expected_mtime"`             // Unix ms from Read or Write
	ExpectedVersion *string `json:"expected_version,omitempty"` // Version from Read or Write
}

type WriteResponse struct {
	MTime   int64  `json:"mtime"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
}

type RenameRequest struct {
	ID   int64  `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// UploadResponse is the answer of an upload; the digests are only filled when the upload
// carried a Checksum (UploadChecked).
type UploadResponse struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
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
	Kind         JobKind  `json:"kind"` // "" (a service before sub-project 4) counts as JobExtract
	State        JobState `json:"state"`
	Path         string   `json:"path"`
	Dest         string   `json:"dest"`
	Bytes        int64    `json:"bytes"`
	Entries      int64    `json:"entries"`
	TotalBytes   int64    `json:"total_bytes"`
	TotalEntries int64    `json:"total_entries"`
	Snapshot     string   `json:"snapshot"` // backup: the new snapshot once done; restore: its source; else ""
	Skipped      int64    `json:"skipped"`  // entries a backup left out (symlinks, special files, hidden or invalid names)
	Error        string   `json:"error"`
	// Code is the wire code of a failed job's error (CodeTooLarge, CodeInsufficientStorage,
	// CodeBusy, CodeUnavailable, ...; "" for other errors and for jobs that did not fail). The
	// service sets it from sub-project 4 on (phase B1); ErrorFromCode maps it to the sentinel.
	Code       string `json:"code"`
	StartedAt  int64  `json:"started_at"`  // Unix ms
	FinishedAt int64  `json:"finished_at"` // 0 while running
}

type JobsResponse struct {
	Job *Job `json:"job"`
}

// JarsRequest is the body of /v1/jars.
type JarsRequest struct {
	ID  int64  `json:"id"`
	Dir string `json:"dir"` // DirPlugins | DirMods
}

// JarKind is what a jar's metadata file says it is.
type JarKind string

const (
	JarKindPlugin JarKind = "plugin"
	JarKindMod    JarKind = "mod"
)

// JarMeta is read best-effort from the jar (plugin.yml, fabric.mod.json, neoforge.mods.toml).
type JarMeta struct {
	Kind    JarKind `json:"kind"`
	ID      string  `json:"id"` // fabric/neoforge mod id; "" for plugins
	Name    string  `json:"name"`
	Version string  `json:"version"`
}

// Jar is one regular file of the listed directory named *.jar or *.jar.disabled.
type Jar struct {
	Name    string   `json:"name"`    // base name incl. ".jar" / ".jar.disabled"
	Enabled bool     `json:"enabled"` // false for ".jar.disabled"
	Size    int64    `json:"size"`
	MTime   int64    `json:"mtime"`  // Unix ms
	SHA256  string   `json:"sha256"` // lowercase hex; "" above MaxJarHashBytes
	SHA512  string   `json:"sha512"` // lowercase hex; "" above MaxJarHashBytes
	Meta    *JarMeta `json:"meta"`   // nil = not readable
}

// JarsResponse lists the jars of one directory.
type JarsResponse struct {
	Jars      []Jar `json:"jars"` // never nil; sorted by name (byte order)
	Truncated bool  `json:"truncated"`
}

// Checksum is the expected digest of an upload (UploadChecked).
type Checksum struct {
	Algo string // AlgoSHA256 | AlgoSHA512
	Hex  string // lowercase hex, 64 resp. 128 characters
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
	// UploadChecked is Upload with an expected digest: the service compares it before the file
	// replaces anything and answers ErrHashMismatch otherwise (an invalid sum is ErrInvalid
	// without a call).
	UploadChecked(ctx context.Context, id int64, path string, overwrite bool, size int64, sum Checksum, body io.Reader) (UploadResponse, error)
	Download(ctx context.Context, id int64, path string) (Download, error)
	Mkdir(ctx context.Context, id int64, path string) error
	Rename(ctx context.Context, req RenameRequest) error
	Delete(ctx context.Context, id int64, path string) error
	Extract(ctx context.Context, req ExtractRequest) (Job, error)
	Job(ctx context.Context, id int64) (*Job, error)
	// Jars lists the jars of DirPlugins or DirMods with checksums and metadata.
	Jars(ctx context.Context, id int64, dir string) (JarsResponse, error)
}
