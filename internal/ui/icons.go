package ui

import (
	"fmt"
	"strings"
)

type Icons struct {
	Step     string
	Build    string
	Diff     string
	Activate string
	Hook     string
	Warn     string
	Stop     string
	Restart  string
	Reload   string
	Start    string
	Running  string
	Done     string
	Planned  string
	Failed   string
	Added    string
	Removed  string
	Updated  string
	Download string
	Upload   string
	Clock    string
	Summary  string
	Gap      string
}

var Unicode = Icons{
	Step:     ">",
	Build:    ">",
	Diff:     ">",
	Activate: ">",
	Hook:     ">",
	Warn:     "!",
	Stop:     "■",
	Restart:  "◆",
	Reload:   "◇",
	Start:    "▸",
	Running:  "▹",
	Done:     "✓",
	Planned:  "○",
	Failed:   "✗",
	Added:    "+",
	Removed:  "−",
	Updated:  "↑",
	Download: "↓",
	Upload:   "↑",
	Clock:    "◔",
	Summary:  "Σ",
	Gap:      " ",
}

var Nerd = func() Icons {
	i := Unicode
	i.Running = ""
	i.Done = ""
	i.Planned = ""
	i.Failed = ""
	i.Added = "\uf067"
	i.Removed = "\uf068"
	i.Updated = "\uf062"
	i.Download = ""
	i.Upload = ""
	i.Clock = ""
	i.Summary = "\U000f04a0"
	i.Gap = " "
	return i
}()

func DetectIcons(getenv func(string) string) (*Icons, error) {
	switch value := getenv("NERD_FONTS"); strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return &Nerd, nil
	case "", "0", "false", "no":
		return &Unicode, nil
	default:
		return nil, fmt.Errorf("NERD_FONTS must be 1 or 0, got %q", value)
	}
}

func (i *Icons) With(icon, text string) string {
	return icon + i.Gap + text
}
