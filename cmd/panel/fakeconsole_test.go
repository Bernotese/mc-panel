package main

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// fakeConsole simulates the agent's console API: fixed RCON answers and one log stream with
// the lines in logLines (then it stays open until closed).
type fakeConsole struct {
	mu       sync.Mutex
	commands []string
	logLines []agentapi.LogLine
	streams  []*fakeLogStream
}

func newFakeConsole() *fakeConsole { return &fakeConsole{} }

func (f *fakeConsole) Rcon(_ context.Context, id int64, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, fmt.Sprintf("%d %s", id, command))
	if command == "list uuids" {
		return "There are 0 of a max of 20 players online: ", nil
	}
	return "ok: " + command, nil
}

func (f *fakeConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{
		Ops: []agentapi.PlayerOp{{UUID: "u1", Name: "Alex", Level: 4}}, Whitelist: []agentapi.PlayerEntry{},
		Bans: []agentapi.PlayerBan{}, Errors: []string{},
	}, nil
}

func (f *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeLogStream{lines: append([]agentapi.LogLine(nil), f.logLines...), closed: make(chan struct{})}
	f.streams = append(f.streams, s)
	return s, nil
}

func (f *fakeConsole) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

type fakeLogStream struct {
	mu     sync.Mutex
	lines  []agentapi.LogLine
	closed chan struct{}
	once   sync.Once
}

func (s *fakeLogStream) Next() (agentapi.LogLine, error) {
	s.mu.Lock()
	if len(s.lines) > 0 {
		l := s.lines[0]
		s.lines = s.lines[1:]
		s.mu.Unlock()
		return l, nil
	}
	s.mu.Unlock()
	<-s.closed
	return agentapi.LogLine{}, io.EOF
}

func (s *fakeLogStream) Running() bool { return true }

func (s *fakeLogStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
