package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/stubbedev/phix/internal/unit"
)

const (
	socketName    = "supervisor.sock"
	lockName      = "supervisor.lock"
	stderrName    = "supervisor.stderr"
	SupervisorLog = "phix-supervisor"
)

type Layout struct {
	Host    unit.Host
	State   string
	Runtime string
}

func (l Layout) Socket() string    { return filepath.Join(l.Runtime, socketName) }
func (l Layout) Lock() string      { return filepath.Join(l.Runtime, lockName) }
func (l Layout) NotifyDir() string { return filepath.Join(l.Runtime, "notify") }
func (l Layout) LogDir() string    { return filepath.Join(l.State, "logs") }
func (l Layout) StampDir() string  { return filepath.Join(l.State, "timers") }
func (l Layout) Stderr() string    { return filepath.Join(l.State, stderrName) }

func DetectLayout(getenv func(string) string) (Layout, error) {
	u, err := user.Current()
	if err != nil {
		return Layout{}, fmt.Errorf("looking up the current user: %w", err)
	}
	home := getenv("HOME")
	if home == "" {
		home = u.HomeDir
	}
	if !filepath.IsAbs(home) {
		return Layout{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return Layout{}, fmt.Errorf("reading the hostname: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return Layout{}, fmt.Errorf("parsing uid %q: %w", u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return Layout{}, fmt.Errorf("parsing gid %q: %w", u.Gid, err)
	}
	xdg := func(name, fallback string) (string, error) {
		v := getenv(name)
		if v == "" {
			return filepath.Join(home, fallback), nil
		}
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("%s=%q is not absolute", name, v)
		}
		return v, nil
	}
	host := unit.Host{Home: home, User: u.Username, UID: uid, GID: gid, Hostname: hostname}
	for _, d := range []struct {
		dst            *string
		name, fallback string
	}{
		{&host.ConfigHome, "XDG_CONFIG_HOME", ".config"},
		{&host.DataHome, "XDG_DATA_HOME", ".local/share"},
		{&host.StateHome, "XDG_STATE_HOME", ".local/state"},
		{&host.CacheHome, "XDG_CACHE_HOME", ".cache"},
	} {
		if *d.dst, err = xdg(d.name, d.fallback); err != nil {
			return Layout{}, err
		}
	}
	host.TempDir = os.TempDir()
	host.RuntimeDir = getenv("XDG_RUNTIME_DIR")
	if host.RuntimeDir == "" {
		host.RuntimeDir = filepath.Join(host.TempDir, "phix-runtime-"+u.Uid)
	}
	l := Layout{
		Host:    host,
		State:   filepath.Join(host.StateHome, "phix", "services"),
		Runtime: filepath.Join(host.RuntimeDir, "phix"),
	}
	if err := l.checkSocketPaths(); err != nil {
		return Layout{}, err
	}
	return l, nil
}

const maxSocketPath = 107

func (l Layout) checkSocketPaths() error {
	longest := l.Socket()
	if n := l.notifyPath(strings.Repeat("0", notifyNameLength)); len(n) > len(longest) {
		longest = n
	}
	if len(longest) > maxSocketPath {
		return fmt.Errorf("the supervisor's socket path %s is %d bytes, over the %d a unix socket allows; set XDG_RUNTIME_DIR (or TMPDIR) to a shorter directory", longest, len(longest), maxSocketPath)
	}
	return nil
}

func (l Layout) prepare() error {
	if err := privateDir(l.Host.RuntimeDir, l.Host.UID); err != nil {
		return err
	}
	for _, d := range []string{l.Runtime, l.NotifyDir()} {
		if err := privateDir(d, l.Host.UID); err != nil {
			return err
		}
	}
	for _, d := range []string{l.LogDir(), l.StampDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}
	return nil
}

func privateDir(dir string, uid int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("checking %s: no ownership information", dir)
	}
	if !info.IsDir() || int(st.Uid) != uid {
		return fmt.Errorf("%s must be a directory owned by uid %d", dir, uid)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New(dir + " is accessible by other users; it must be mode 0700")
	}
	return nil
}
