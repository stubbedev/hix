package sops

import (
	"regexp"
	"strings"

	"github.com/stubbedev/phix/internal/ui"
)

var (
	sopsSynced    = regexp.MustCompile(`synced with new keys`)
	sopsUpToDate  = regexp.MustCompile(`already up to date`)
	sopsRecipient = regexp.MustCompile(`^\s*(\+\+\+|---)\s*(\S+)`)
)

func sopsFormat(st ui.Style, line string) (string, bool) {
	ic := st.Icons()
	if m := sopsRecipient.FindStringSubmatch(line); m != nil {
		if m[1] == "+++" {
			return st.Paint(ic.Added, ui.Green) + " " + m[2], true
		}
		return st.Paint(ic.Removed, ui.Red) + " " + m[2], true
	}
	switch {
	case sopsSynced.MatchString(line):
		return st.Paint(ic.Done, ui.Green) + " re-encrypted for the new recipients", true
	case sopsUpToDate.MatchString(line):
		return st.Paint(ic.Done, ui.Green) + " " + st.Paint("already up to date", ui.Faint), true
	}
	return ui.Notable(st, strings.TrimSpace(sopsLogPrefix.ReplaceAllString(line, "")))
}

var sopsLogPrefix = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} |\[SOPS\]\s+\w+\[\d+\]\s*)`)
