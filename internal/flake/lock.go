package flake

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/phix/internal/ui"
)

const (
	shortRevLen = 7
	dateLayout  = "2006-01-02"
)

type pin struct {
	id       string
	modified time.Time
}

type lockFileJSON struct {
	Root  string `json:"root"`
	Nodes map[string]struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
		Locked *struct {
			Rev          string `json:"rev"`
			NarHash      string `json:"narHash"`
			LastModified int64  `json:"lastModified"`
		} `json:"locked"`
	} `json:"nodes"`
}

func readPins(path string) (map[string]pin, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]pin{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	pins, err := parsePins(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return pins, nil
}

func parsePins(data []byte) (map[string]pin, error) {
	var lock lockFileJSON
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	pins := make(map[string]pin)
	var walk func(node, prefix string, onPath map[string]bool) error
	walk = func(node, prefix string, onPath map[string]bool) error {
		if onPath[node] {
			return fmt.Errorf("input cycle through node %q", node)
		}
		onPath[node] = true
		defer delete(onPath, node)
		for _, name := range slices.Sorted(maps.Keys(lock.Nodes[node].Inputs)) {
			raw := lock.Nodes[node].Inputs[name]
			if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
				continue
			}
			var target string
			if err := json.Unmarshal(raw, &target); err != nil {
				return fmt.Errorf("input %s: %w", prefix+name, err)
			}
			child, ok := lock.Nodes[target]
			if !ok {
				return fmt.Errorf("input %s points at missing node %q", prefix+name, target)
			}
			if l := child.Locked; l != nil {
				pins[prefix+name] = pin{id: pinID(l.Rev, l.NarHash), modified: time.Unix(l.LastModified, 0).UTC()}
			}
			if err := walk(target, prefix+name+"/", onPath); err != nil {
				return err
			}
		}
		return nil
	}
	if _, ok := lock.Nodes[lock.Root]; !ok {
		return nil, fmt.Errorf("root node %q is missing", lock.Root)
	}
	if err := walk(lock.Root, "", make(map[string]bool)); err != nil {
		return nil, err
	}
	return pins, nil
}

func pinID(rev, narHash string) string {
	id := rev
	if id == "" {
		id = strings.TrimPrefix(narHash, "sha256-")
	}
	return id[:min(len(id), shortRevLen)]
}

type changeKind int

const (
	changeUpdated changeKind = iota + 1
	changeAdded
	changeRemoved
)

type inputChange struct {
	path     string
	kind     changeKind
	old, new pin
}

func diffPins(old, new map[string]pin) []inputChange {
	var changes []inputChange
	for path, n := range new {
		o, existed := old[path]
		switch {
		case !existed:
			changes = append(changes, inputChange{path: path, kind: changeAdded, new: n})
		case o.id != n.id:
			changes = append(changes, inputChange{path: path, kind: changeUpdated, old: o, new: n})
		}
	}
	for path, o := range old {
		if _, kept := new[path]; !kept {
			changes = append(changes, inputChange{path: path, kind: changeRemoved, old: o})
		}
	}
	kinds := make(map[string]changeKind, len(changes))
	for _, c := range changes {
		kinds[c.path] = c.kind
	}
	changes = slices.DeleteFunc(changes, func(c inputChange) bool {
		i := strings.LastIndexByte(c.path, '/')
		return c.kind != changeUpdated && i >= 0 && kinds[c.path[:i]] == c.kind
	})
	slices.SortFunc(changes, func(a, b inputChange) int { return cmp.Compare(a.path, b.path) })
	return changes
}

func renderChanges(st ui.Style, changes []inputChange) []string {
	width := 0
	for _, c := range changes {
		width = max(width, len(c.path))
	}
	ic := st.Icons()
	lines := make([]string, len(changes))
	for i, c := range changes {
		name := ui.PadRight(c.path, width)
		switch c.kind {
		case changeUpdated:
			lines[i] = st.Paint(ic.Updated, ui.Cyan) + " " + name + "  " + st.Paint(c.old.id, ui.Faint) + st.Paint(" → ", ui.Gray) + st.Paint(c.new.id, ui.Green) + "  " + st.Paint(dateChange(c.old.modified, c.new.modified), ui.Gray)
		case changeAdded:
			lines[i] = st.Paint(ic.Added, ui.Green) + " " + name + "  " + st.Paint(c.new.id, ui.Green) + "  " + st.Paint(c.new.modified.Format(dateLayout), ui.Gray)
		case changeRemoved:
			lines[i] = st.Paint(ic.Removed, ui.Red) + " " + name + "  " + st.Paint(c.old.id, ui.Faint)
		}
	}
	return lines
}

func dateChange(old, new time.Time) string {
	o, n := old.Format(dateLayout), new.Format(dateLayout)
	if o == n {
		return n
	}
	return o + " → " + n
}

var lockChatter = []*regexp.Regexp{
	regexp.MustCompile(`^warning: (updating|creating) lock file `),
	regexp.MustCompile(`^• (Updated|Added|Removed) input '`),
	regexp.MustCompile(`^('|→ '|follows ')`),
}

func updateFormat(st ui.Style, line string) (string, bool) {
	for _, re := range lockChatter {
		if re.MatchString(line) {
			return "", false
		}
	}
	return ui.Notable(st, line)
}
