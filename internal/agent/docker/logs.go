package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// LogsOptions selects the part of a container log that ContainerLogs returns.
type LogsOptions struct {
	// Follow keeps the stream open for new lines until the container stops.
	Follow bool
	// Tail is the number of lines from the end; < 0 means all.
	Tail int
	// Since is a Docker timestamp in nanoseconds since the Unix epoch (inclusive); 0 means none.
	Since int64
}

func (o LogsOptions) query() url.Values {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "follow": {"0"}, "tail": {"all"}}
	if o.Follow {
		q.Set("follow", "1")
	}
	if o.Tail >= 0 {
		q.Set("tail", strconv.Itoa(o.Tail))
	}
	if o.Since > 0 {
		q.Set("since", fmt.Sprintf("%d.%09d", o.Since/int64(time.Second), o.Since%int64(time.Second)))
	}
	return q
}

// ContainerLogs opens the log of a container without TTY as a multiplexed stream (read it
// with NewDemuxer). Every line starts with its RFC 3339 timestamp (9 fraction digits)
// and a space. Only ctx bounds the call.
func (c *Client) ContainerLogs(ctx context.Context, id string, o LogsOptions) (io.ReadCloser, error) {
	return c.stream(ctx, http.MethodGet, containerPath(id, "logs"), o.query(), nil)
}
