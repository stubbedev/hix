package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/stubbedev/phix/internal/unit"
)

var ErrAlreadyRunning = errors.New("another phix service supervisor is already running")

func Supervise(ctx context.Context, layout Layout, program Program, inherited []string, ready func(error)) error {
	if err := layout.prepare(); err != nil {
		ready(err)
		return err
	}
	lock, err := os.OpenFile(layout.Lock(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		err = fmt.Errorf("opening %s: %w", layout.Lock(), err)
		ready(err)
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			ready(ErrAlreadyRunning)
			return nil
		}
		err = fmt.Errorf("locking %s: %w", layout.Lock(), err)
		ready(err)
		return err
	}
	if err := os.Remove(layout.Socket()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		err = fmt.Errorf("removing the stale socket %s: %w", layout.Socket(), err)
		ready(err)
		return err
	}
	listener, err := net.Listen("unix", layout.Socket())
	if err != nil {
		err = fmt.Errorf("listening on %s: %w", layout.Socket(), err)
		ready(err)
		return err
	}
	defer func() {
		if err := os.Remove(layout.Socket()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "phix supervisor: removing %s: %v\n", layout.Socket(), err)
		}
	}()
	if err := os.Chmod(layout.Socket(), 0o600); err != nil {
		err = fmt.Errorf("restricting %s: %w", layout.Socket(), err)
		ready(err)
		return errors.Join(err, listener.Close())
	}

	m := newManager(layout, program, inherited)
	if err := m.loadUnits(); err != nil {
		ready(err)
		return errors.Join(err, listener.Close())
	}
	go m.loop()
	m.post(func() {
		m.slog("started (pid %d), %d units loaded, %d failed to load", os.Getpid(), len(m.set.Units), len(m.set.Errors))
		root, err := unit.ParseName(unit.DefaultTarget)
		if err != nil {
			panic(err)
		}
		m.enqueueStart(root, func(err error) {
			if err != nil {
				m.slog("starting %s: %v", root, err)
			}
		})
	})
	ready(nil)

	var conns sync.WaitGroup
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conns.Go(func() { m.serve(conn) })
		}
	}()
	stopOnCancel := context.AfterFunc(ctx, func() { m.post(m.beginShutdown) })
	defer stopOnCancel()
	<-m.done
	m.slog("stopped")
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("closing %s: %w", layout.Socket(), err)
	}
	return nil
}

func (m *Manager) serve(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			m.reportErr(fmt.Errorf("closing a client connection: %w", err))
		}
	}()
	enc := json.NewEncoder(conn)
	send := func(r Response) bool { return enc.Encode(r) == nil }
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		send(Response{Error: fmt.Sprintf("decoding the request: %v", err)})
		return
	}
	switch req.Op {
	case OpPing:
		send(Response{Done: true})
	case OpStatus:
		send(m.ask(func() Response { return m.status(req.Units) }))
	case OpStart, OpStop, OpRestart, OpReload:
		send(m.control(req.Op, req.Units))
	case OpSync:
		m.serveSync(req, send)
	case OpDaemonReload:
		send(m.ask(func() Response {
			if err := m.loadUnits(); err != nil {
				return Response{Error: err.Error()}
			}
			return Response{Done: true}
		}))
	case OpResetFailed:
		send(m.ask(func() Response { return m.resetFailed(req.Units) }))
	case OpShutdown:
		m.post(m.beginShutdown)
		<-m.done
		send(Response{Done: true})
	case OpFollow:
		m.serveLogs(conn, req, send)
	default:
		send(Response{Error: fmt.Sprintf("unknown operation %q", req.Op)})
	}
}

func (m *Manager) ask(f func() Response) Response {
	reply := make(chan Response, 1)
	m.post(func() { reply <- f() })
	select {
	case r := <-reply:
		return r
	case <-m.done:
		return Response{Error: "the supervisor is shutting down"}
	}
}

func (m *Manager) resolve(args []string) ([]unit.Name, error) {
	names := make([]unit.Name, 0, len(args))
	for _, a := range args {
		n, err := unit.ParseServiceArg(a)
		if err != nil {
			return nil, err
		}
		if m.states[n] == nil {
			if err := m.set.Errors[n]; err != nil {
				return nil, fmt.Errorf("%s: %w", n, err)
			}
			return nil, fmt.Errorf("%s: no such unit", n)
		}
		names = append(names, n)
	}
	return names, nil
}

func (m *Manager) control(op Op, args []string) Response {
	if len(args) == 0 {
		return Response{Error: fmt.Sprintf("%s needs at least one unit", op)}
	}
	results := make(chan error, len(args))
	var resolveErr error
	m.post(func() {
		names, err := m.resolve(args)
		if err != nil {
			resolveErr = err
			for range args {
				results <- nil
			}
			return
		}
		for _, n := range names {
			st := m.states[n]
			done := func(err error) { results <- err }
			switch {
			case m.shuttingDown:
				done(fmt.Errorf("%s: the supervisor is shutting down", n))
			case (op == OpStart || op == OpRestart) && st.u.RefuseManualStart:
				done(fmt.Errorf("%s refuses manual start (RefuseManualStart=yes)", n))
			case (op == OpStop || op == OpRestart) && st.u.RefuseManualStop:
				done(fmt.Errorf("%s refuses manual stop (RefuseManualStop=yes)", n))
			case op == OpStart:
				m.enqueueStart(n, done)
			case op == OpStop:
				m.enqueueStop(n, false, done)
			case op == OpRestart:
				m.enqueueStop(n, true, done)
			case op == OpReload:
				if m.jobs[n] != nil {
					done(fmt.Errorf("%s has a pending %s job", n, m.jobs[n].kind))
					continue
				}
				m.reload(st, done)
			}
		}
	})
	var failures []string
	for range args {
		select {
		case err := <-results:
			if err != nil {
				failures = append(failures, err.Error())
			}
		case <-m.done:
			return Response{Error: "the supervisor shut down before the jobs finished"}
		}
	}
	if resolveErr != nil {
		return Response{Error: resolveErr.Error()}
	}
	return Response{Failures: failures, Done: true}
}

func (m *Manager) serveSync(req Request, send func(Response) bool) {
	if req.Executable != "" && filepath.Clean(req.Executable) != m.program.Executable {
		r := m.ask(func() Response {
			var changes []Change
			for _, n := range sortedNames(m.states) {
				if st := m.states[n]; st.running() && n.Kind() == unit.Service {
					changes = append(changes, Change{Action: ActionRestart, Unit: n.String()})
				}
			}
			m.slog("phix changed from %s to %s, restarting the supervisor", m.program.Executable, req.Executable)
			m.beginShutdown()
			return Response{Restarting: true, Changes: changes}
		})
		send(r)
		<-m.done
		return
	}
	reply := make(chan Response, 1)
	m.post(func() {
		m.program.ExecPath = req.ExecPath
		m.sync(func(changes []Change, failures []string, err error) {
			if err != nil {
				reply <- Response{Error: err.Error()}
				return
			}
			reply <- Response{Changes: changes, Failures: failures, Done: true}
		})
	})
	select {
	case r := <-reply:
		send(r)
	case <-m.done:
		send(Response{Error: "the supervisor shut down during the sync"})
	}
}

func (m *Manager) serveLogs(conn net.Conn, req Request, send func(Response) bool) {
	if len(req.Units) > 1 {
		send(Response{Error: "follow takes at most one unit"})
		return
	}
	name, err := LogName(req.Units)
	if err != nil {
		send(Response{Error: err.Error()})
		return
	}
	lines := req.Lines
	if lines == 0 {
		lines = -1
	}
	tail, ch, cancel, err := m.journal.Follow(name, lines)
	if err != nil {
		send(Response{Error: err.Error()})
		return
	}
	defer cancel()
	for _, l := range tail {
		if !send(Response{Line: l}) {
			return
		}
	}
	closed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}()
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				send(Response{Error: "the log reader fell behind and was dropped"})
				return
			}
			if !send(Response{Line: l}) {
				return
			}
		case <-closed:
			return
		case <-m.done:
			send(Response{Done: true})
			return
		}
	}
}

func (m *Manager) status(args []string) Response {
	names := sortedNames(m.states)
	if len(args) > 0 {
		resolved, err := m.resolve(args)
		if err != nil {
			return Response{Error: err.Error()}
		}
		names = resolved
	} else {
		for n := range m.set.Errors {
			names = append(names, n)
		}
		slices.SortFunc(names, func(a, b unit.Name) int { return strings.Compare(a.String(), b.String()) })
	}
	want := wanted(m.set)
	out := make([]UnitStatus, 0, len(names))
	for _, n := range names {
		st := m.states[n]
		if st == nil {
			out = append(out, UnitStatus{Name: n.String(), Kind: n.Kind().String(), LoadError: m.set.Errors[n].Error(), Active: string(stateInactive), Sub: "dead"})
			continue
		}
		s := UnitStatus{
			Name:        n.String(),
			Kind:        n.Kind().String(),
			Description: st.u.Description,
			Fragment:    st.u.Fragment,
			Active:      string(st.active),
			Sub:         st.sub,
			Since:       st.since.UTC(),
			Result:      st.result,
			StatusText:  st.statusText,
			Wanted:      want[n],
			Ignored:     st.u.Ignored,
		}
		if st.main != nil {
			s.MainPID = st.main.pid
		}
		if j := m.jobs[n]; j != nil {
			s.Job = j.kind.String()
			if j.thenStart {
				s.Job = "restart"
			}
		}
		if sock := st.u.Socket; sock != nil {
			s.Triggers = sock.Service.String()
			for _, l := range sock.Listen {
				s.Listen = append(s.Listen, l.String())
			}
		}
		if t := st.u.Timer; t != nil {
			s.Triggers = t.Unit.String()
			if run := st.timer; run != nil {
				s.NextElapse = utcOrZero(run.next)
				s.LastTrigger = utcOrZero(run.lastTrigger)
				if s.LastTrigger.IsZero() {
					s.LastTrigger = utcOrZero(run.persisted)
				}
			}
		}
		out = append(out, s)
	}
	return Response{Status: out, Done: true}
}

func utcOrZero(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func (m *Manager) resetFailed(args []string) Response {
	names := sortedNames(m.states)
	if len(args) > 0 {
		resolved, err := m.resolve(args)
		if err != nil {
			return Response{Error: err.Error()}
		}
		names = resolved
	}
	for _, n := range names {
		st := m.states[n]
		st.starts = nil
		if st.active == stateFailed {
			st.result = ""
			m.setState(st, stateInactive, "dead")
		}
	}
	return Response{Done: true}
}
