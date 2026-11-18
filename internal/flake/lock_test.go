package flake

import (
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/phix/internal/ui"
)

const lockBefore = `{
  "root": "root",
  "version": 7,
  "nodes": {
    "root": {"inputs": {"nixpkgs": "nixpkgs", "lys": "lys", "old": "old", "local": "local"}},
    "nixpkgs": {"locked": {"type": "github", "rev": "aaaaaaaaaaaa", "narHash": "sha256-x", "lastModified": 1794700000}},
    "lys": {"inputs": {"nixpkgs": ["nixpkgs"], "mesa": "mesa"}, "locked": {"rev": "1111111111", "lastModified": 1794900000}},
    "mesa": {"locked": {"rev": "cccccccccc", "lastModified": 1794900000}},
    "old": {"inputs": {"dep": "oldDep"}, "locked": {"rev": "dddddddddd", "lastModified": 1794000000}},
    "oldDep": {"locked": {"rev": "eeeeeeeeee", "lastModified": 1794000000}},
    "local": {"locked": {"type": "path", "narHash": "sha256-LocalHash123", "lastModified": 1794000000}}
  }
}`

const lockAfter = `{
  "root": "root",
  "version": 7,
  "nodes": {
    "root": {"inputs": {"nixpkgs": "nixpkgs", "lys": "lys", "new": "new", "local": "local"}},
    "nixpkgs": {"locked": {"type": "github", "rev": "aaaaaaaaaaaa", "narHash": "sha256-x", "lastModified": 1794700000}},
    "lys": {"inputs": {"nixpkgs": ["nixpkgs"], "mesa": "mesa"}, "locked": {"rev": "2222222222", "lastModified": 1795000000}},
    "mesa": {"locked": {"rev": "ffffffffff", "lastModified": 1795000000}},
    "new": {"inputs": {"dep": "newDep"}, "locked": {"rev": "9999999999", "lastModified": 1795000000}},
    "newDep": {"locked": {"rev": "8888888888", "lastModified": 1795000000}},
    "local": {"locked": {"type": "path", "narHash": "sha256-LocalHash123", "lastModified": 1794000000}}
  }
}`

func mustPins(t *testing.T, data string) map[string]pin {
	t.Helper()
	pins, err := parsePins([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return pins
}

func TestParsePinsSkipsFollowsAndNamesNestedInputs(t *testing.T) {
	pins := mustPins(t, lockBefore)
	want := []string{"local", "lys", "lys/mesa", "nixpkgs", "old", "old/dep"}
	got := make([]string, 0, len(pins))
	for path := range pins {
		got = append(got, path)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("pins = %v, want %v", got, want)
	}
	if id := pins["local"].id; id != "LocalHa" {
		t.Fatalf("path input id = %q, want the short narHash", id)
	}
	if id := pins["nixpkgs"].id; id != "aaaaaaa" {
		t.Fatalf("github input id = %q, want the short rev", id)
	}
}

func TestParsePinsRejectsDanglingInput(t *testing.T) {
	if _, err := parsePins([]byte(`{"root":"root","nodes":{"root":{"inputs":{"x":"gone"}}}}`)); err == nil {
		t.Fatal("dangling input accepted")
	}
}

func TestDiffPins(t *testing.T) {
	changes := diffPins(mustPins(t, lockBefore), mustPins(t, lockAfter))
	type summary struct {
		path string
		kind changeKind
	}
	got := make([]summary, len(changes))
	for i, c := range changes {
		got[i] = summary{c.path, c.kind}
	}
	want := []summary{
		{"lys", changeUpdated},
		{"lys/mesa", changeUpdated},
		{"new", changeAdded},
		{"old", changeRemoved},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
}

func TestRenderChanges(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }
	lines := renderChanges(ui.Style{}, []inputChange{
		{path: "lys", kind: changeUpdated, old: pin{"a3ba1e5", day(17)}, new: pin{"5d3b1f7", day(18)}},
		{path: "nixpkgs", kind: changeUpdated, old: pin{"1111111", day(18)}, new: pin{"2222222", day(18)}},
		{path: "phix", kind: changeAdded, new: pin{"f327bef", day(18)}},
		{path: "hix", kind: changeRemoved, old: pin{"4f9085f", day(18)}},
	})
	want := []string{
		"↑ lys      a3ba1e5 → 5d3b1f7  2026-11-17 → 2026-11-18",
		"↑ nixpkgs  1111111 → 2222222  2026-11-18",
		"+ phix     f327bef  2026-11-18",
		"− hix      4f9085f",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("rendered:\n%q\nwant:\n%q", lines, want)
	}
}

func TestUpdateFormatHidesLockChatter(t *testing.T) {
	for _, line := range []string{
		`warning: updating lock file "/etc/nixos/dotfiles/flake.lock":`,
		`• Updated input 'lys':`,
		`    'github:stubbedev/lys/a3ba1e5?narHash=sha256-Y6%3D' (2026-11-18)`,
		`  → 'github:stubbedev/lys/5d3b1f7?narHash=sha256-ve%3D' (2026-11-18)`,
		`• Added input 'phix/nixpkgs':`,
		`    follows 'nixpkgs'`,
	} {
		if out, ok := updateFormat(ui.Style{}, line); ok {
			t.Errorf("lock chatter %q shown as %q", line, out)
		}
	}
	out, ok := updateFormat(ui.Style{}, "warning: Git tree '/etc/nixos/dotfiles' is dirty")
	if want := ui.Unicode.Warn + " Git tree '/etc/nixos/dotfiles' is dirty"; !ok || out != want {
		t.Fatalf("warning shown as %q, %v; want %q", out, ok, want)
	}
}
