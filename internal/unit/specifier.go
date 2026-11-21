package unit

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	sdunit "github.com/coreos/go-systemd/v22/unit"
)

type Host struct {
	Home       string
	User       string
	UID        int
	GID        int
	Hostname   string
	ConfigHome string
	DataHome   string
	StateHome  string
	CacheHome  string
	RuntimeDir string
	TempDir    string
}

func (h Host) UnitDirs() []string {
	return []string{
		filepath.Join(h.ConfigHome, "systemd", "user"),
		filepath.Join(h.DataHome, "systemd", "user"),
	}
}

func (h Host) EnvironmentDir() string { return filepath.Join(h.ConfigHome, "environment.d") }

func (h Host) ManagerConfig() string { return filepath.Join(h.ConfigHome, "systemd", "user.conf") }

type specifiers struct {
	host Host
	name Name
}

func (s specifiers) expand(in string) (string, error) {
	i := strings.IndexByte(in, '%')
	if i < 0 {
		return in, nil
	}
	var b strings.Builder
	b.Grow(len(in))
	for i >= 0 {
		b.WriteString(in[:i])
		if i+1 >= len(in) {
			return "", fmt.Errorf("%q: trailing %% without a specifier", in)
		}
		v, err := s.value(in[i+1])
		if err != nil {
			return "", fmt.Errorf("%q: %w", in, err)
		}
		b.WriteString(v)
		in = in[i+2:]
		i = strings.IndexByte(in, '%')
	}
	b.WriteString(in)
	return b.String(), nil
}

func (s specifiers) value(c byte) (string, error) {
	h, n := s.host, s.name
	switch c {
	case '%':
		return "%", nil
	case 'n':
		return n.String(), nil
	case 'N':
		return n.Stem(), nil
	case 'p':
		return n.Prefix(), nil
	case 'P':
		return sdunit.UnitNameUnescape(n.Prefix()), nil
	case 'i':
		return n.Instance(), nil
	case 'I':
		return sdunit.UnitNameUnescape(n.Instance()), nil
	case 'j':
		return lastComponent(n.Prefix()), nil
	case 'J':
		return sdunit.UnitNameUnescape(lastComponent(n.Prefix())), nil
	case 'f':
		raw := n.Instance()
		if raw == "" {
			raw = n.Prefix()
		}
		return sdunit.UnitNamePathUnescape(raw), nil
	case 'h':
		return h.Home, nil
	case 'u':
		return h.User, nil
	case 'U':
		return strconv.Itoa(h.UID), nil
	case 'G':
		return strconv.Itoa(h.GID), nil
	case 'H':
		return h.Hostname, nil
	case 'l':
		short, _, _ := strings.Cut(h.Hostname, ".")
		return short, nil
	case 't':
		return h.RuntimeDir, nil
	case 'S':
		return h.StateHome, nil
	case 'C':
		return h.CacheHome, nil
	case 'E':
		return h.ConfigHome, nil
	case 'L':
		return filepath.Join(h.StateHome, "log"), nil
	case 'T', 'V':
		return h.TempDir, nil
	}
	return "", fmt.Errorf("specifier %%%c is not supported", c)
}

func lastComponent(prefix string) string {
	if _, last, ok := strings.CutLast(prefix, "-"); ok {
		return last
	}
	return prefix
}
