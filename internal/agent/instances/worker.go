package instances

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

const (
	// MaxOpenOperations bounds the operations per instance (running + waiting).
	MaxOpenOperations = 4
	// MaxTrackedInstances bounds the per-instance state the agent keeps in memory.
	MaxTrackedInstances = 256

	maxLastErrorRunes = 500
	redacted          = "[redacted]"
)

type jobKind string

const (
	jobEnsure jobKind = "ensure"
	jobPower  jobKind = "power"
	jobRemove jobKind = "remove"
)

type job struct {
	kind   jobKind
	ensure agentapi.EnsureRequest
	action agentapi.PowerAction
}

// instanceState is the in-memory worker state of one instance. It exists while
// operations are queued or running, and afterwards only to keep a last_error.
type instanceState struct {
	pending   []job
	running   bool
	op        agentapi.Operation
	lastError string
	name      string
}

func (m *Manager) enqueue(ctx context.Context, id int64, j job) error {
	if !m.dockerAvailable(ctx) {
		return agentapi.ErrDockerUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("%w: agent is shutting down", agentapi.ErrDockerUnavailable)
	}
	st := m.inst[id]
	if st == nil {
		if len(m.inst) >= MaxTrackedInstances {
			return agentapi.ErrBusy
		}
		st = &instanceState{}
		m.inst[id] = st
	}
	open := len(st.pending)
	if st.running {
		open++
	}
	if open >= MaxOpenOperations {
		return agentapi.ErrBusy
	}
	if j.kind == jobEnsure {
		st.name = j.ensure.Name
	}
	st.pending = append(st.pending, j)
	m.log.Info("instance operation queued", "id", id, "op", j.kind, "action", j.action, "revision", j.ensure.Revision)
	if !st.running {
		st.running = true
		m.active++
		go m.work(id)
	}
	return nil
}

// work drains the queue of one instance; exactly one worker runs per instance.
func (m *Manager) work(id int64) {
	for {
		m.mu.Lock()
		st := m.inst[id]
		if m.closed {
			st.pending = nil
		}
		if len(st.pending) == 0 {
			st.running = false
			st.op = agentapi.OpNone
			if st.lastError == "" {
				delete(m.inst, id)
			}
			m.active--
			m.idle.Broadcast()
			m.mu.Unlock()
			return
		}
		j := st.pending[0]
		st.pending = st.pending[1:]
		st.op = initialOp(j)
		m.mu.Unlock()

		err := m.run(id, j)

		m.mu.Lock()
		st.op = agentapi.OpNone
		if err != nil {
			st.lastError = sanitizeError(err, secretsOf(j)...)
		} else {
			st.lastError = ""
		}
		msg := st.lastError
		m.mu.Unlock()
		if err != nil {
			m.log.Warn("instance operation failed", "id", id, "op", j.kind, "action", j.action, "err", msg)
		} else {
			m.log.Info("instance operation done", "id", id, "op", j.kind, "action", j.action)
		}
	}
}

func initialOp(j job) agentapi.Operation {
	switch {
	case j.kind == jobEnsure:
		return agentapi.OpCreating
	case j.kind == jobRemove:
		return agentapi.OpRemoving
	case j.action == agentapi.PowerStart:
		return agentapi.OpStarting
	default:
		return agentapi.OpStopping
	}
}

func secretsOf(j job) []string {
	if j.kind == jobEnsure && j.ensure.Spec.CurseForgeAPIKey != nil {
		return []string{*j.ensure.Spec.CurseForgeAPIKey}
	}
	return nil
}

func (m *Manager) run(id int64, j job) error {
	var err error
	switch j.kind {
	case jobEnsure:
		err = m.ensure(m.ctx, j.ensure)
	case jobPower:
		err = m.power(m.ctx, id, j.action)
	case jobRemove:
		err = m.remove(m.ctx, id)
	}
	if errors.Is(err, docker.ErrUnavailable) {
		m.setDocker(false, err)
	}
	return err
}

func (m *Manager) setOp(id int64, op agentapi.Operation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.inst[id]; st != nil {
		st.op = op
	}
}

// sanitizeError makes an error safe for last_error: secrets replaced, control
// characters flattened, at most 500 characters.
func sanitizeError(err error, secrets ...string) string {
	s := err.Error()
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, redacted)
		}
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
	if utf8.RuneCountInString(s) > maxLastErrorRunes {
		s = string([]rune(s)[:maxLastErrorRunes-1]) + "…"
	}
	return s
}

// Wait blocks until no operation is queued or running.
func (m *Manager) Wait() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.active > 0 {
		m.idle.Wait()
	}
}

// Close rejects new operations, cancels running Docker calls, drops queued
// operations and waits for the workers to finish.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	m.Wait()
}
