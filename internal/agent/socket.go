package agent

import (
	"net"
	"os"

	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// ErrAlreadyRunning is returned by Listen when another process accepts
// connections on the socket path.
var ErrAlreadyRunning = unixsock.ErrInUse

// Listen removes a stale socket file, listens on path, sets the socket's group
// to gid (gid < 0 leaves it unchanged) and its permissions to mode. Closing the
// returned listener removes the socket file.
func Listen(path string, mode os.FileMode, gid int) (net.Listener, error) {
	return unixsock.Listen(path, mode, gid)
}
