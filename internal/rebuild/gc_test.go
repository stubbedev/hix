package rebuild

import (
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
		{"finding garbage collector roots...", "", false},
		{"deleting '/nix/store/aaaa-hello-2.12'", "", false},
		{"deleting unused links...", "", false},
		{"note: currently hard linking saves 1.20 GiB", "currently hard linking saves 1.20 GiB", true},
		{"1234 store paths deleted, 4.20 GiB freed", ic.Done + " 1234 store paths deleted, 4.20 GiB freed", true},
		{"error: cannot delete path", ic.Failed + " cannot delete path", true},
	}
	for _, c := range cases {
		got, shown := gcFormat(ui.Style{}, c.line)
		if shown != c.shown || got != c.want {
			t.Errorf("gcFormat(%q) = %q, %v; want %q, %v", c.line, got, shown, c.want, c.shown)
		}
	}
}
