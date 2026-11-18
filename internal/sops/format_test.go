package sops

import (
	"testing"

	"github.com/stubbedev/phix/internal/ui"
)

func TestSopsFormat(t *testing.T) {
	ic := ui.Unicode
	cases := []struct {
		line, want string
		shown      bool
	}{
		{"2026/11/18 17:00:00 Syncing keys for file /flake/secrets/token", "", false},
		{"The following changes will be made to the file's groups:", "", false},
		{"Group 1", "", false},
		{"    age1old", "", false},
		{"+++ age1newrecipient", ic.Added + " age1newrecipient", true},
		{"--- age1gonerecipient", ic.Removed + " age1gonerecipient", true},
		{"2026/11/18 17:00:00 File /flake/secrets/token synced with new keys", ic.Done + " re-encrypted for the new recipients", true},
		{"2026/11/18 17:00:00 File /flake/secrets/token already up to date", ic.Done + " already up to date", true},
		{"[SOPS]   WARN[0000] warning: config has no creation rules", ic.Warn + " config has no creation rules", true},
	}
	for _, c := range cases {
		got, shown := sopsFormat(ui.Style{}, c.line)
		if shown != c.shown || got != c.want {
			t.Errorf("sopsFormat(%q) = %q, %v; want %q, %v", c.line, got, shown, c.want, c.shown)
		}
	}
}
