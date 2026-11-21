package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	notifyDatagramMax = 4096
	notifyNameLength  = 16
)

func (l Layout) notifyPath(name string) string {
	return filepath.Join(l.NotifyDir(), name+".sock")
}

type notifier struct {
	path string
	conn *net.UnixConn
}

func (m *Manager) openNotifier(st *state) (*notifier, error) {
	m.closeNotifier(st)
	sum := sha256.Sum256([]byte(st.name().String()))
	path := m.layout.notifyPath(hex.EncodeToString(sum[:notifyNameLength/2]))
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing the stale notify socket %s: %w", path, err)
	}
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, fmt.Errorf("creating the notify socket %s: %w", path, err)
	}
	n := &notifier{path: path, conn: conn}
	go func() {
		buf := make([]byte, notifyDatagramMax)
		for {
			size, _, err := conn.ReadFromUnix(buf)
			if err != nil {
				return
			}
			msg := string(buf[:size])
			m.post(func() {
				if st.notify == n {
					m.notified(st, msg)
				}
			})
		}
	}()
	return n, nil
}

func (m *Manager) closeNotifier(st *state) {
	n := st.notify
	if n == nil {
		return
	}
	st.notify = nil
	if err := n.conn.Close(); err != nil {
		m.ulog(st, "closing the notify socket: %v", err)
	}
	if err := os.Remove(n.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.ulog(st, "removing the notify socket: %v", err)
	}
}

func (m *Manager) notified(st *state, msg string) {
	for field := range strings.SplitSeq(msg, "\n") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "READY":
			if value == "1" {
				m.ready(st)
			}
		case "RELOADING":
			if value == "1" && st.active == stateActive {
				m.setState(st, stateReloading, "reload-notify")
			}
		case "STATUS":
			st.statusText = value
		case "STOPPING":
			if value == "1" {
				m.ulog(st, "Service reported it is stopping")
			}
		case "ERRNO":
			st.statusText = "errno " + value
		case "MAINPID":
			m.ulog(st, "Ignoring MAINPID=%s: the main process must stay the one phix started", value)
		}
	}
}

func (m *Manager) ready(st *state) {
	switch {
	case st.active == stateActivating && st.sub == "start" && !st.ready:
		st.ready = true
		m.startPost(st)
	case st.active == stateReloading:
		m.disarm(st)
		m.setState(st, stateActive, "running")
		m.ulog(st, "Reloaded")
		if st.reloadWaiter != nil {
			st.reloadWaiter(nil)
			st.reloadWaiter = nil
		}
	}
}
