package waker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// maxTracked bounds the hosts remembered for WakeEvery. mc-router only sends the names of
// sleeping dev instances here, so the set stays small; the bound is defence in depth.
const maxTracked = 1024

// Config configures Serve. Every field except Listen is required.
type Config struct {
	// Listen is the address the waker subcommand listens on; Serve takes the listener.
	Listen string
	// MOTD is the description of the status answer, Kick the reason of the login disconnect.
	MOTD, Kick string
	// MaxConns bounds the open connections, MaxPerIP those of one remote address; a
	// connection above either limit is closed right after accept.
	MaxConns, MaxPerIP int
	// Deadline bounds a whole connection, reads and writes included.
	Deadline time.Duration
	// WakeEvery is the minimum gap between two wake lines of one host.
	WakeEvery time.Duration
	// Out receives one line "wake <host>\n" per reported login attempt and nothing else.
	Out io.Writer
	// Clock decides WakeEvery. Socket deadlines use the wall clock (the kernel enforces them).
	Clock clock.Clock
}

func (c Config) check() error {
	switch {
	case c.Out == nil:
		return errors.New("waker: Out is required")
	case c.Clock == nil:
		return errors.New("waker: Clock is required")
	case c.MaxConns < 1 || c.MaxPerIP < 1:
		return errors.New("waker: MaxConns and MaxPerIP must be positive")
	case c.Deadline <= 0 || c.WakeEvery < 0:
		return errors.New("waker: Deadline must be positive and WakeEvery not negative")
	}
	return nil
}

type server struct {
	c      Config
	kick   []byte
	wall   clock.Clock
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]string // open connection → remote address key
	perIP  map[string]int
	woken  map[string]time.Time // host → last wake line
}

// Serve accepts connections on ln until ctx is done, then closes ln and every open
// connection and returns nil after all handlers ended. Each connection gets one handshake:
// a status request is answered with the MOTD and its ping echoed; a login attempt writes a
// wake line (at most one per host per WakeEvery) and is refused with the Kick text. The
// legacy ping and anything else that is not a valid handshake close the connection.
func Serve(ctx context.Context, ln net.Listener, c Config) error {
	if err := c.check(); err != nil {
		return err
	}
	kick, err := loginDisconnect(c.Kick)
	if err != nil {
		return err
	}
	s := &server{c: c, kick: kick, wall: clock.Real{}, conns: map[net.Conn]string{}, perIP: map[string]int{}, woken: map[string]time.Time{}}
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
		s.closeAll()
	})
	defer stop()
	backoff := time.Duration(0)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				return err
			}
			// Out of file descriptors and the like: back off instead of spinning.
			backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0
		if !s.admit(conn) {
			_ = conn.Close()
			continue
		}
		s.wg.Go(func() {
			defer s.release(conn)
			s.handle(conn)
		})
	}
}

func remoteKey(a net.Addr) string {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.String()
	}
	return a.String()
}

// admit registers conn unless a limit is reached or the server is shutting down.
func (s *server) admit(conn net.Conn) bool {
	key := remoteKey(conn.RemoteAddr())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.conns) >= s.c.MaxConns || s.perIP[key] >= s.c.MaxPerIP {
		return false
	}
	s.conns[conn] = key
	s.perIP[key]++
	return true
}

func (s *server) release(conn net.Conn) {
	_ = conn.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.conns[conn]
	if !ok {
		return
	}
	delete(s.conns, conn)
	if s.perIP[key]--; s.perIP[key] <= 0 {
		delete(s.perIP, key)
	}
}

func (s *server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for conn := range s.conns {
		_ = conn.Close()
	}
}

func (s *server) handle(conn net.Conn) {
	if err := conn.SetDeadline(s.wall.Now().Add(s.c.Deadline)); err != nil {
		return
	}
	r := bufio.NewReaderSize(conn, 512)
	h, err := ReadHandshake(r)
	if err != nil {
		return
	}
	if h.Next == NextStatus {
		s.status(conn, r, h)
		return
	}
	s.login(conn, r, h)
}

// status answers the status request and then echoes the ping.
func (s *server) status(conn net.Conn, r *bufio.Reader, h Handshake) {
	id, body, err := readPacket(r)
	if err != nil || id != 0x00 || len(body) != 0 {
		return
	}
	resp, err := statusResponse(h.Protocol, s.c.MOTD)
	if err != nil {
		return
	}
	if _, err := conn.Write(resp); err != nil {
		return
	}
	id, body, err = readPacket(r)
	if err != nil || id != 0x01 || len(body) != 8 {
		return
	}
	_, _ = conn.Write(packet(0x01, body))
}

// login reports the attempt, reads the login start the client sends right after the
// handshake (so closing does not reset the connection under an unread packet) and refuses it.
func (s *server) login(conn net.Conn, r *bufio.Reader, h Handshake) {
	s.wake(h.Host)
	if _, _, err := readPacket(r); err != nil {
		return
	}
	_, _ = conn.Write(s.kick)
}

// wake writes the wake line of host unless one went out within WakeEvery.
func (s *server) wake(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.c.Clock.Now()
	if last, ok := s.woken[host]; ok && now.Sub(last) < s.c.WakeEvery {
		return
	}
	if len(s.woken) >= maxTracked {
		for h, last := range s.woken {
			if now.Sub(last) >= s.c.WakeEvery {
				delete(s.woken, h)
			}
		}
		if len(s.woken) >= maxTracked {
			return
		}
	}
	s.woken[host] = now
	// One Write per line under the lock: lines never interleave.
	_, _ = fmt.Fprintf(s.c.Out, "wake %s\n", host)
}
