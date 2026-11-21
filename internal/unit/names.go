package unit

import (
	"fmt"
	"strings"
)

type Kind int

const (
	Service Kind = iota + 1
	Timer
	Target
	Socket
	Path
	Mount
	Automount
	Swap
	Slice
	Scope
	Device
)

var kindSuffixes = []struct {
	kind   Kind
	suffix string
}{
	{Service, "service"},
	{Timer, "timer"},
	{Target, "target"},
	{Socket, "socket"},
	{Path, "path"},
	{Mount, "mount"},
	{Automount, "automount"},
	{Swap, "swap"},
	{Slice, "slice"},
	{Scope, "scope"},
	{Device, "device"},
}

func (k Kind) String() string {
	for _, ks := range kindSuffixes {
		if ks.kind == k {
			return ks.suffix
		}
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

func (k Kind) Supported() bool {
	return k == Service || k == Timer || k == Target || k == Socket
}

func parseKind(suffix string) (Kind, bool) {
	for _, ks := range kindSuffixes {
		if ks.suffix == suffix {
			return ks.kind, true
		}
	}
	return 0, false
}

type Name struct {
	prefix   string
	instance string
	template bool
	kind     Kind
}

const maxNameLength = 255

func ParseName(s string) (Name, error) {
	if s == "" || len(s) > maxNameLength {
		return Name{}, fmt.Errorf("unit name %q: must be 1 to %d bytes", s, maxNameLength)
	}
	dot := strings.LastIndexByte(s, '.')
	if dot <= 0 {
		return Name{}, fmt.Errorf("unit name %q has no type suffix such as .service", s)
	}
	kind, ok := parseKind(s[dot+1:])
	if !ok {
		return Name{}, fmt.Errorf("unit name %q: unknown unit type %q", s, s[dot+1:])
	}
	stem := s[:dot]
	for i := range len(stem) {
		if !nameByte(stem[i]) {
			return Name{}, fmt.Errorf("unit name %q: invalid character %q", s, stem[i])
		}
	}
	n := Name{prefix: stem, kind: kind}
	at := strings.IndexByte(stem, '@')
	if at < 0 {
		return n, nil
	}
	if at == 0 {
		return Name{}, fmt.Errorf("unit name %q: empty prefix before @", s)
	}
	if strings.IndexByte(stem[at+1:], '@') >= 0 {
		return Name{}, fmt.Errorf("unit name %q: more than one @", s)
	}
	n.prefix = stem[:at]
	n.instance = stem[at+1:]
	n.template = n.instance == ""
	return n, nil
}

func ParseServiceArg(s string) (Name, error) {
	if dot := strings.LastIndexByte(s, '.'); dot > 0 {
		if _, ok := parseKind(s[dot+1:]); ok {
			return ParseName(s)
		}
	}
	return ParseName(s + "." + Service.String())
}

func nameByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte(":-_.\\@", c) >= 0
}

func (n Name) String() string {
	if n.instance != "" || n.template {
		return n.prefix + "@" + n.instance + "." + n.kind.String()
	}
	return n.prefix + "." + n.kind.String()
}

func (n Name) Kind() Kind { return n.kind }

func (n Name) Prefix() string { return n.prefix }

func (n Name) Instance() string { return n.instance }

func (n Name) IsTemplate() bool { return n.template }

func (n Name) IsInstance() bool { return n.instance != "" }

func (n Name) Template() Name {
	return Name{prefix: n.prefix, template: true, kind: n.kind}
}

func (n Name) Stem() string {
	return strings.TrimSuffix(n.String(), "."+n.kind.String())
}

func (n Name) WithKind(k Kind) Name {
	n.kind = k
	return n
}

func (n Name) IsZero() bool { return n.kind == 0 }
