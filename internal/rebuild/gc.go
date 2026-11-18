package rebuild

import (
	"regexp"

	"github.com/stubbedev/phix/internal/ui"
)

var (
	gcFreed    = regexp.MustCompile(`^\d+ store paths deleted, .+ freed$`)
	gcProfile  = regexp.MustCompile(`^removing old generations of profile (.+)$`)
	gcSavings  = regexp.MustCompile(`^note: (currently hard linking saves .+)$`)
	gcDeleting = regexp.MustCompile(`^(deleting|removing profile version|removing stale link|finding garbage collector roots|deleting garbage|deleting unused links)`)
)

func gcFormat(st ui.Style, line string) (string, bool) {
	ic := st.Icons()
	switch {
	case gcFreed.MatchString(line):
		return st.Paint(ic.Done, ui.Green) + " " + line, true
	case gcProfile.MatchString(line):
		return st.Paint(ic.Removed, ui.Red) + " " + st.Paint("old generations of "+gcProfile.FindStringSubmatch(line)[1], ui.Faint), true
	case gcSavings.MatchString(line):
		return st.Paint(gcSavings.FindStringSubmatch(line)[1], ui.Faint), true
	case gcDeleting.MatchString(line):
		return "", false
	}
	return ui.Notable(st, line)
}
