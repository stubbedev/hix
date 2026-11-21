package supervisor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/phix/internal/unit"
)

type listener struct {
	spec   unit.Listen
	closer io.Closer
	file   *os.File
}

type socketWatch struct {
	stop *os.File
}

func listenOne(spec unit.Listen, s *unit.SocketConfig) (listener, error) {
	if spec.Unix != "" {
		if err := os.MkdirAll(filepath.Dir(spec.Unix), s.DirectoryMode); err != nil {
			return listener{}, fmt.Errorf("creating the directory of %s: %w", spec.Unix, err)
		}
		if info, err := os.Lstat(spec.Unix); err == nil && info.Mode()&fs.ModeSocket != 0 {
			if err := os.Remove(spec.Unix); err != nil {
				return listener{}, fmt.Errorf("removing the stale socket %s: %w", spec.Unix, err)
			}
		}
	}
	var (
		closer io.Closer
		file   *os.File
		err    error
	)
	switch spec.Network() {
	case "unix", "unixpacket":
		var l *net.UnixListener
		if l, err = net.ListenUnix(spec.Network(), &net.UnixAddr{Name: spec.Unix, Net: spec.Network()}); err == nil {
			l.SetUnlinkOnClose(false)
			closer = l
			file, err = l.File()
		}
	case "unixgram":
		var c *net.UnixConn
		if c, err = net.ListenUnixgram("unixgram", &net.UnixAddr{Name: spec.Unix, Net: "unixgram"}); err == nil {
			closer = c
			file, err = c.File()
		}
	case "tcp":
		var addr *net.TCPAddr
		var l *net.TCPListener
		if addr, err = net.ResolveTCPAddr("tcp", spec.Address); err == nil {
			if l, err = net.ListenTCP("tcp", addr); err == nil {
				closer = l
				file, err = l.File()
			}
		}
	default:
		var addr *net.UDPAddr
		var c *net.UDPConn
		if addr, err = net.ResolveUDPAddr("udp", spec.Address); err == nil {
			if c, err = net.ListenUDP("udp", addr); err == nil {
				closer = c
				file, err = c.File()
			}
		}
	}
	if err != nil {
		if closer != nil {
			err = errors.Join(err, closer.Close())
		}
		return listener{}, fmt.Errorf("listening on %s: %w", spec, err)
	}
	if spec.Unix != "" {
		if err := os.Chmod(spec.Unix, s.SocketMode); err != nil {
			return listener{}, errors.Join(fmt.Errorf("setting the mode of %s: %w", spec.Unix, err), file.Close(), closer.Close())
		}
	}
	return listener{spec: spec, closer: closer, file: file}, nil
}

func (m *Manager) startSocket(st *state) error {
	s := st.u.Socket
	for _, spec := range s.Listen {
		l, err := listenOne(spec, s)
		if err != nil {
			m.closeListeners(st)
			return err
		}
		st.listeners = append(st.listeners, l)
	}
	m.setState(st, stateActive, "listening")
	m.ulog(st, "Listening on %s", listenSummary(st))
	m.unitActivated(st)
	if svc := m.states[s.Service]; svc == nil || !svc.running() {
		m.armSocket(st)
	}
	return nil
}

func listenSummary(st *state) string {
	parts := make([]string, len(st.listeners))
	for i, l := range st.listeners {
		parts[i] = l.spec.String()
	}
	return strings.Join(parts, ", ")
}

func (m *Manager) stopSocket(st *state) {
	m.disarmSocket(st)
	m.closeListeners(st)
	if st.running() {
		m.setState(st, stateInactive, "dead")
		m.unitDeactivated(st)
	}
}

func (m *Manager) closeListeners(st *state) {
	for _, l := range st.listeners {
		if err := errors.Join(l.file.Close(), l.closer.Close()); err != nil && !errors.Is(err, net.ErrClosed) {
			m.ulog(st, "closing %s: %v", l.spec, err)
		}
		if l.spec.Unix != "" && st.u.Socket.RemoveOnStop {
			if err := os.Remove(l.spec.Unix); err != nil && !errors.Is(err, fs.ErrNotExist) {
				m.ulog(st, "removing %s: %v", l.spec.Unix, err)
			}
		}
	}
	st.listeners = nil
}

func (m *Manager) armSocket(st *state) {
	if st.watch != nil || len(st.listeners) == 0 {
		return
	}
	r, w, err := os.Pipe()
	if err != nil {
		m.ulog(st, "creating the watch pipe: %v", err)
		m.failSocket(st)
		return
	}
	watch := &socketWatch{stop: w}
	st.watch = watch
	m.setState(st, stateActive, "listening")
	fds := make([]unix.PollFd, 0, len(st.listeners)+1)
	for _, l := range st.listeners {
		fds = append(fds, unix.PollFd{Fd: int32(l.file.Fd()), Events: unix.POLLIN}) //nolint:gosec
	}
	fds = append(fds, unix.PollFd{Fd: int32(r.Fd()), Events: unix.POLLIN}) //nolint:gosec
	go func() {
		defer r.Close()
		triggered, err := pollUntilReady(fds)
		m.post(func() {
			if st.watch != watch {
				return
			}
			st.watch = nil
			if closeErr := watch.stop.Close(); closeErr != nil {
				m.ulog(st, "closing the watch pipe: %v", closeErr)
			}
			switch {
			case err != nil:
				m.ulog(st, "waiting for connections: %v", err)
				m.failSocket(st)
			case triggered:
				m.socketTriggered(st)
			}
		})
	}()
}

func pollUntilReady(fds []unix.PollFd) (bool, error) {
	for {
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		stop := fds[len(fds)-1]
		if stop.Revents != 0 {
			return false, nil
		}
		for _, fd := range fds[:len(fds)-1] {
			if fd.Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
				return false, fmt.Errorf("listening socket fd %d reported an error", fd.Fd)
			}
			if fd.Revents != 0 {
				return true, nil
			}
		}
	}
}

func (m *Manager) disarmSocket(st *state) {
	w := st.watch
	if w == nil {
		return
	}
	st.watch = nil
	if _, err := w.stop.Write([]byte{0}); err != nil {
		m.ulog(st, "stopping the connection watch: %v", err)
	}
	if err := w.stop.Close(); err != nil {
		m.ulog(st, "closing the watch pipe: %v", err)
	}
}

func (m *Manager) socketTriggered(st *state) {
	target := st.u.Socket.Service
	svc := m.states[target]
	if svc == nil || svc.removed {
		m.ulog(st, "Cannot activate %s: it is not loaded", target)
		m.failSocket(st)
		return
	}
	m.setState(st, stateActive, "running")
	if svc.running() || m.jobs[target] != nil {
		return
	}
	m.ulog(st, "Incoming traffic, starting %s", target)
	m.enqueueStart(target, func(err error) {
		if err != nil {
			m.ulog(st, "Starting %s failed: %v", target, err)
			m.failSocket(st)
		}
	})
}

func (m *Manager) failSocket(st *state) {
	m.disarmSocket(st)
	m.closeListeners(st)
	st.result = "service-start-failed"
	m.setState(st, stateFailed, "failed")
	m.unitDeactivated(st)
}

func (m *Manager) retrigger(st *state) {
	switch {
	case st.u.Timer != nil:
		m.schedule(st)
	case st.u.Socket != nil && st.active == stateActive:
		svc := m.states[st.u.Socket.Service]
		if svc != nil && svc.running() {
			m.disarmSocket(st)
			m.setState(st, stateActive, "running")
			return
		}
		m.armSocket(st)
	}
}

func (m *Manager) socketFiles(st *state) ([]*os.File, []string) {
	var (
		files []*os.File
		names []string
	)
	for _, n := range m.graph.triggeredBy[st.name()] {
		sock := m.states[n]
		if sock == nil || sock.u.Socket == nil {
			continue
		}
		for _, l := range sock.listeners {
			files = append(files, l.file)
			names = append(names, sock.u.Socket.FDName)
		}
	}
	return files, names
}

func listenEnv(env unit.Env, names []string) unit.Env {
	env = env.Clone()
	delete(env, "LISTEN_PID")
	env["LISTEN_FDS"] = strconv.Itoa(len(names))
	env["LISTEN_FDNAMES"] = strings.Join(names, ":")
	return env
}
