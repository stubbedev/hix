package rebuild

import (
	"strings"
	"testing"

	"github.com/stubbedev/phix/internal/ui"
)

func TestGCFormat(t *testing.T) {
	ic := ui.Unicode
	cases := []struct {
		line, want string
		shown      bool
	}{
		{"removing old generations of profile /nix/var/nix/profiles/system", ic.Removed + " old generations of /nix/var/nix/profiles/system", true},
		{"removing profile version 380", "", false},
		{"  removing profile version 514", "", false},
		{"\x1b[1mremoving profile version 515\x1b[0m", "", false},
		{"deleting garbage within specified paths...", "", false},
		{"removing stale link from /nix/var/nix/gcroots/auto/x to /tmp/result", "", false},
		{"deleting '/nix/store/aaaa-test-failed-1'", "", false},
		{"finding garbage collector roots...", "", false},
		{"deleting '/nix/store/aaaa-hello-2.12'", "", false},
		{"deleting unused links...", "", false},
		{"note: currently hard linking saves 1.20 GiB", "currently hard linking saves 1.20 GiB", true},
		{"1234 store paths deleted, 4.20 GiB freed", ic.Done + " 1234 store paths deleted, 4.20 GiB freed", true},
		{"error: cannot delete path", ic.Failed + " cannot delete path", true},
	}
	for _, c := range cases {
		var out strings.Builder
		g := ui.Printer{W: &out}.Gutter(gcFormat)
		if _, err := g.Write([]byte(c.line + "\n")); err != nil {
			t.Fatal(err)
		}
		got, shown := strings.TrimPrefix(strings.TrimSuffix(out.String(), "\n"), "│ "), out.Len() > 0
		if shown != c.shown || got != c.want {
			t.Errorf("gcFormat(%q) = %q, %v; want %q, %v", c.line, got, shown, c.want, c.shown)
		}
	}
}
