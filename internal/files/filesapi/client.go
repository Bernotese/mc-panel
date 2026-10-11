package filesapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"
)

const (
	// CallTimeout bounds every small call (list, read, write, mkdir, rename, jobs).
	CallTimeout = 10 * time.Second
	// DeleteTimeout bounds a delete: removing a world folder with many thousand files can take
	// minutes on a slow disk.
	DeleteTimeout = 10 * time.Minute
	// ExtractTimeout bounds the synchronous pre-check of an extract (central directory of up to
	// max_extract_entries entries); the job itself runs in the background.
	ExtractTimeout = 5 * time.Minute
	// HealthTimeout bounds the health check.
	HealthTimeout = 2 * time.Second
	// DownloadHeaderTimeout bounds the wait for download headers; the stream itself has no
	// deadline. Uploads have no header timeout: the service answers after the whole body.
	DownloadHeaderTimeout = 10 * time.Second

	baseURL          = "http://files"
	maxResponseBytes = 32 << 20 // a read answer carries up to 5 MiB of escaped text
	maxErrorBytes    = 64 << 10
)

// UnixClient talks to mcpanel-files over its unix socket. Safe for concurrent use.
type UnixClient struct {
	hc                    *http.Client
	callTimeout           time.Duration
	deleteTimeout         time.Duration
	extractTimeout        time.Duration
	downloadHeaderTimeout time.Duration
}

var _ Client = (*UnixClient)(nil)

func NewClient(socketPath string) *UnixClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		// Proxy stays nil: HTTP(S)_PROXY must never redirect file traffic.
	}
	return &UnixClient{
		hc: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		callTimeout:           CallTimeout,
		deleteTimeout:         DeleteTimeout,
		extractTimeout:        ExtractTimeout,
		downloadHeaderTimeout: DownloadHeaderTimeout,
	}
}

func checkID(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", ErrInvalid)
	}
	return nil
}

// clean validates id and every path before anything is sent.
func clean(id int64, paths ...*string) error {
	if err := checkID(id); err != nil {
		return err
	}
	for _, p := range paths {
		c, err := CleanPath(*p)
		if err != nil {
			return err
		}
		*p = c
	}
	return nil
}

func (c *UnixClient) Health(ctx context.Context) error {
	var out HealthResponse
	if err := c.call(ctx, HealthTimeout, http.MethodGet, PathHealth, nil, &out); err != nil {
		return err
	}
	if out.Status != "ok" {
		return fmt.Errorf("%w: health status %q", ErrUnavailable, out.Status)
	}
	return nil
}

func (c *UnixClient) List(ctx context.Context, id int64, p string) (ListResponse, error) {
	if err := clean(id, &p); err != nil {
		return ListResponse{}, err
	}
	var out ListResponse
	err := c.call(ctx, c.callTimeout, http.MethodPost, PathList, PathRequest{ID: id, Path: p}, &out)
	if out.Entries == nil {
		out.Entries = []Entry{}
	}
	return out, err
}

func (c *UnixClient) Read(ctx context.Context, id int64, p string) (ReadResponse, error) {
	if err := clean(id, &p); err != nil {
		return ReadResponse{}, err
	}
	var out ReadResponse
	err := c.call(ctx, c.callTimeout, http.MethodPost, PathRead, PathRequest{ID: id, Path: p}, &out)
	return out, err
}

func (c *UnixClient) Write(ctx context.Context, req WriteRequest) (WriteResponse, error) {
	if err := clean(req.ID, &req.Path); err != nil {
		return WriteResponse{}, err
	}
	var out WriteResponse
	err := c.call(ctx, c.callTimeout, http.MethodPost, PathWrite, req, &out)
	return out, err
}

func (c *UnixClient) Mkdir(ctx context.Context, id int64, p string) error {
	if err := clean(id, &p); err != nil {
		return err
	}
	return c.call(ctx, c.callTimeout, http.MethodPost, PathMkdir, PathRequest{ID: id, Path: p}, nil)
}

func (c *UnixClient) Rename(ctx context.Context, req RenameRequest) error {
	if err := clean(req.ID, &req.From, &req.To); err != nil {
		return err
	}
	return c.call(ctx, c.callTimeout, http.MethodPost, PathRename, req, nil)
}

func (c *UnixClient) Delete(ctx context.Context, id int64, p string) error {
	if err := clean(id, &p); err != nil {
		return err
	}
	return c.call(ctx, c.deleteTimeout, http.MethodPost, PathDelete, PathRequest{ID: id, Path: p}, nil)
}

// Extract starts an extract job. The service checks the archive synchronously (central
// directory: names, symlinks, sizes, entries, destination, disk space); those errors come back
// directly (ErrInvalid, ErrNotRegular, ErrExists, ErrTooLarge, ErrInsufficientStorage, ErrBusy)
// and no job is created. Only a passed check answers 202 with the running job.
func (c *UnixClient) Extract(ctx context.Context, req ExtractRequest) (Job, error) {
	if err := clean(req.ID, &req.Path, &req.Dest); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.extractTimeout, http.MethodPost, PathExtract, req, &out)
	return out, err
}

func (c *UnixClient) Job(ctx context.Context, id int64) (*Job, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	var out JobsResponse
	q := url.Values{"id": {strconv.FormatInt(id, 10)}}
	if err := c.call(ctx, c.callTimeout, http.MethodGet, PathJobs+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Job, nil
}

// Upload streams body (exactly size bytes) to path. There is no timeout besides ctx: the
// service answers only after the whole body arrived (spec §4, up to 4 GiB).
func (c *UnixClient) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (UploadResponse, error) {
	if err := clean(id, &p); err != nil {
		return UploadResponse{}, err
	}
	if size < 0 {
		return UploadResponse{}, fmt.Errorf("%w: upload size must be known", ErrInvalid)
	}
	ow := "0"
	if overwrite {
		ow = "1"
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "path": {p}, "overwrite": {ow}}
	// An empty file goes out as http.NoBody: net/http would otherwise probe the reader and could
	// fall back to chunked encoding, which the service refuses (Content-Length is required).
	var rb io.Reader = http.NoBody
	if size > 0 {
		rb = io.NopCloser(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+PathUpload+"?"+q.Encode(), rb)
	if err != nil {
		return UploadResponse{}, fmt.Errorf("files: build request: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return UploadResponse{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out UploadResponse
	return out, decode(resp, &out)
}

// Download opens path for streaming. Headers must arrive within DownloadHeaderTimeout; the body
// has no deadline (ctx only). Closing the body releases the connection.
func (c *UnixClient) Download(ctx context.Context, id int64, p string) (Download, error) {
	if err := clean(id, &p); err != nil {
		return Download{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "path": {p}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+PathDownload+"?"+q.Encode(), nil)
	if err != nil {
		cancel()
		return Download{}, fmt.Errorf("files: build request: %w", err)
	}
	timer := time.AfterFunc(c.downloadHeaderTimeout, cancel)
	resp, err := c.hc.Do(req)
	if !timer.Stop() {
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		return Download{}, fmt.Errorf("%w: download: no response within %s", ErrUnavailable, c.downloadHeaderTimeout)
	}
	if err != nil {
		cancel()
		return Download{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	// Only a 200 with a known kind is a download; every other answer (also 204/206) is an error,
	// so a caller never gets a Download without a Body.
	kind := resp.Header.Get(HeaderKind)
	if resp.StatusCode != http.StatusOK || (kind != "file" && kind != "dir") {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return Download{}, decode(resp, nil)
		}
		return Download{}, fmt.Errorf("%w: download: unexpected answer %d (kind %q)", ErrUnavailable, resp.StatusCode, kind)
	}
	d := Download{Body: &cancelBody{ReadCloser: resp.Body, cancel: cancel}, Size: resp.ContentLength}
	if kind == "dir" {
		d.IsDir, d.Size = true, -1
	}
	if p != "" {
		d.Name = path.Base(p)
	}
	return d, nil
}

// cancelBody releases the request context when the body is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// call sends one small JSON request (in may be nil) and decodes the answer into out (may be nil).
func (c *UnixClient) call(ctx context.Context, timeout time.Duration, method, target string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // keep config text compact (see MaxJSONBytes)
		if err := enc.Encode(in); err != nil {
			return fmt.Errorf("files: encode request: %w", err)
		}
		body = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+target, body)
	if err != nil {
		return fmt.Errorf("files: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decode(resp, out)
}

// decode turns a 2xx answer into out (nil = ignore the body) and anything else into a
// *StatusError.
func decode(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		var er ErrorResponse
		msg := http.StatusText(resp.StatusCode)
		if json.Unmarshal(data, &er) == nil && er.Error != "" {
			msg = er.Error
		}
		return &StatusError{Status: resp.StatusCode, Code: er.Code, Message: msg}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBytes))
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", ErrUnavailable, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", ErrUnavailable, maxResponseBytes)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decode response: %w", ErrUnavailable, err)
	}
	return nil
}
