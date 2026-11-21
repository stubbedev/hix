package unit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	sdunit "github.com/coreos/go-systemd/v22/unit"
)

const DefaultTarget = "default.target"

var builtinTargets = map[string]Deps{
	"default.target": {Requires: mustNames("basic.target"), After: mustNames("basic.target")},
	"basic.target": {
		Wants: mustNames("sockets.target", "timers.target", "paths.target"),
		After: mustNames("sockets.target", "timers.target", "paths.target"),
	},
	"sockets.target":                {},
	"timers.target":                 {},
	"paths.target":                  {},
	"shutdown.target":               {},
	"exit.target":                   {},
	"graphical-session.target":      {},
	"graphical-session-pre.target":  {},
	"xdg-desktop-autostart.target":  {},
	"bluetooth.target":              {},
	"printer.target":                {},
	"smartcard.target":              {},
	"sound.target":                  {},
	"tray.target":                   {},
	"network-online.target":         {},
	"local-fs.target":               {},
	"remote-fs.target":              {},
	"systemd-tmpfiles-setup.target": {},
}

func mustNames(names ...string) []Name {
	out := make([]Name, len(names))
	for i, s := range names {
		n, err := ParseName(s)
		if err != nil {
			panic(err)
		}
		out[i] = n
	}
	return out
}

type Set struct {
	Units  map[Name]*Unit
	Errors map[Name]error
}

func (s *Set) Names() []Name {
	names := slices.Collect(maps.Keys(s.Units))
	names = append(names, slices.Collect(maps.Keys(s.Errors))...)
	slices.SortFunc(names, func(a, b Name) int { return strings.Compare(a.String(), b.String()) })
	return names
}

type index struct {
	fragments map[string]string
	dropins   map[string]map[string]string
	wants     map[string][]string
	requires  map[string][]string
}

func scan(dirs []string) (index, error) {
	idx := index{
		fragments: map[string]string{},
		dropins:   map[string]map[string]string{},
		wants:     map[string][]string{},
		requires:  map[string][]string{},
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return index{}, fmt.Errorf("reading unit directory %s: %w", dir, err)
		}
		for _, e := range entries {
			if err := idx.add(dir, e.Name()); err != nil {
				return index{}, err
			}
		}
	}
	return idx, nil
}

func (idx index) add(dir, name string) error {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.IsDir() {
		if _, err := ParseName(name); err == nil {
			if _, seen := idx.fragments[name]; !seen {
				idx.fragments[name] = path
			}
		}
		return nil
	}
	switch {
	case strings.HasSuffix(name, ".wants"):
		return idx.members(idx.wants, strings.TrimSuffix(name, ".wants"), path)
	case strings.HasSuffix(name, ".requires"):
		return idx.members(idx.requires, strings.TrimSuffix(name, ".requires"), path)
	case strings.HasSuffix(name, ".d"):
		confs, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("reading drop-in directory %s: %w", path, err)
		}
		set := idx.dropins[name]
		if set == nil {
			set = map[string]string{}
			idx.dropins[name] = set
		}
		for _, c := range confs {
			if strings.HasSuffix(c.Name(), ".conf") {
				if _, seen := set[c.Name()]; !seen {
					set[c.Name()] = filepath.Join(path, c.Name())
				}
			}
		}
	}
	return nil
}

func (idx index) members(dst map[string][]string, owner, path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	for _, e := range entries {
		if _, err := ParseName(e.Name()); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !slices.Contains(dst[owner], e.Name()) {
			dst[owner] = append(dst[owner], e.Name())
		}
	}
	return nil
}

func Load(host Host, local *time.Location) (*Set, error) {
	idx, err := scan(host.UnitDirs())
	if err != nil {
		return nil, err
	}
	set := &Set{Units: map[Name]*Unit{}, Errors: map[Name]error{}}
	var queue []Name
	enqueue := func(s string) error {
		n, err := ParseName(s)
		if err != nil {
			return err
		}
		queue = append(queue, n)
		return nil
	}
	seeds := slices.Collect(maps.Keys(idx.fragments))
	for owner, members := range idx.wants {
		seeds = append(seeds, owner)
		seeds = append(seeds, members...)
	}
	for owner, members := range idx.requires {
		seeds = append(seeds, owner)
		seeds = append(seeds, members...)
	}
	seeds = append(seeds, DefaultTarget)
	slices.Sort(seeds)
	for _, s := range seeds {
		if err := enqueue(s); err != nil {
			return nil, err
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.IsTemplate() || set.Units[n] != nil || set.Errors[n] != nil {
			continue
		}
		u, err := loadOne(idx, n, host, local)
		if err != nil {
			set.Errors[n] = err
			continue
		}
		set.Units[n] = u
		queue = append(queue, references(u)...)
	}
	return set, nil
}

func references(u *Unit) []Name {
	d := u.Deps
	out := slices.Concat(d.Wants, d.Requires, d.Requisite, d.BindsTo, d.PartOf, d.Conflicts, u.OnFailure, u.OnSuccess)
	if u.Timer != nil {
		out = append(out, u.Timer.Unit)
	}
	if u.Socket != nil {
		out = append(out, u.Socket.Service)
	}
	return out
}

func loadOne(idx index, n Name, host Host, local *time.Location) (*Unit, error) {
	if !n.Kind().Supported() {
		return nil, fmt.Errorf("%s units are not supported", n.Kind())
	}
	b := newBuilder(n, host, local)
	digest := sha256.New()
	fragment, ok := idx.fragments[n.String()]
	if !ok && n.IsInstance() {
		fragment, ok = idx.fragments[n.Template().String()]
	}
	switch {
	case ok:
		if err := b.read(fragment, digest); err != nil {
			return nil, err
		}
		b.u.Fragment = fragment
	case isBuiltin(n):
		b.u.Deps = cloneDeps(builtinTargets[n.String()])
		digest.Write([]byte("builtin " + n.String()))
	default:
		return nil, errors.New("unit file not found")
	}
	for _, path := range dropInPaths(idx, n) {
		if err := b.read(path, digest); err != nil {
			return nil, err
		}
		b.u.DropIns = append(b.u.DropIns, path)
	}
	if err := addMembers(b.u, idx); err != nil {
		return nil, err
	}
	if err := b.finish(); err != nil {
		return nil, fmt.Errorf("%s: %w", b.source(), err)
	}
	b.u.Digest = hex.EncodeToString(digest.Sum(nil))
	return b.u, nil
}

func isBuiltin(n Name) bool {
	_, ok := builtinTargets[n.String()]
	return ok
}

func cloneDeps(d Deps) Deps {
	return Deps{
		Wants:     slices.Clone(d.Wants),
		Requires:  slices.Clone(d.Requires),
		Requisite: slices.Clone(d.Requisite),
		BindsTo:   slices.Clone(d.BindsTo),
		PartOf:    slices.Clone(d.PartOf),
		Conflicts: slices.Clone(d.Conflicts),
		After:     slices.Clone(d.After),
		Before:    slices.Clone(d.Before),
	}
}

func (b *builder) source() string {
	if b.u.Fragment != "" {
		return b.u.Fragment
	}
	return b.u.Name.String()
}

func (b *builder) read(path string, digest hash.Hash) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	digest.Write([]byte(filepath.Base(path) + "\x00"))
	digest.Write(data)
	digest.Write([]byte{0})
	opts, err := sdunit.DeserializeOptions(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, o := range opts {
		value := strings.TrimSpace(strings.ReplaceAll(o.Value, "\\\n", " "))
		if err := b.apply(o.Section, o.Name, value); err != nil {
			return fmt.Errorf("%s: [%s] %s: %w", path, o.Section, o.Name, err)
		}
	}
	return nil
}

func dropInPaths(idx index, n Name) []string {
	dirs := []string{n.Kind().String() + ".d"}
	if n.IsInstance() {
		dirs = append(dirs, n.Template().String()+".d")
	}
	dirs = append(dirs, n.String()+".d")
	merged := map[string]string{}
	for _, d := range dirs {
		maps.Copy(merged, idx.dropins[d])
	}
	out := make([]string, 0, len(merged))
	for _, name := range slices.Sorted(maps.Keys(merged)) {
		out = append(out, merged[name])
	}
	return out
}

func addMembers(u *Unit, idx index) error {
	for _, link := range []struct {
		members []string
		dst     *[]Name
	}{{idx.wants[u.Name.String()], &u.Deps.Wants}, {idx.requires[u.Name.String()], &u.Deps.Requires}} {
		for _, m := range link.members {
			n, err := ParseName(m)
			if err != nil {
				return err
			}
			if !slices.Contains(*link.dst, n) {
				*link.dst = append(*link.dst, n)
			}
		}
	}
	return nil
}
