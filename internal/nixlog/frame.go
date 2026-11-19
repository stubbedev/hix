package nixlog

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/phix/internal/ui"
)

const (
	barWidth          = 20
	maxTransferName   = 32
	spareRows         = 1
	minCollapsedChain = 2
)

type View struct {
	Style  ui.Style
	Width  int
	Height int
}

type footerLine struct {
	joined string
	alone  string
	body   string
}

func (s *State) Frame(v View, now time.Time) []string {
	rows := s.tree(v.Style, now)
	footer := s.footer(v.Style, now)
	if budget := v.Height - len(footer) - spareRows; len(rows) > budget {
		rows = s.fitRows(v.Style, now, max(budget, 0))
	}
	lines := rows
	for i, f := range footer {
		corner := f.alone
		if len(rows) > 0 || i > 0 {
			corner = f.joined
		}
		lines = append(lines, v.Style.Paint(corner, ui.Gray)+f.body)
	}
	for i, line := range lines {
		lines[i] = ui.Truncate(line, v.Width-1)
	}
	return lines
}

func (s *State) Summary(st ui.Style, now time.Time) []string {
	builds := s.Builds()
	if !builds.Any() && s.Copies.Expected == 0 {
		return nil
	}
	ic := st.Icons()
	elapsed := formatDuration(now.Sub(s.started))
	if builds.Failed > 0 {
		head := fmt.Sprintf("%d failed build(s)", builds.Failed)
		if cancelled := len(s.withStatus(Cancelled)); cancelled > 0 {
			head += fmt.Sprintf(", %d cancelled", cancelled)
		}
		return append([]string{st.Paint(ic.With(ic.Failed, head+" after "+elapsed), ui.Bold, ui.Red)}, failureReport(st, s.withStatus(Failed))...)
	}
	var parts []string
	if builds.Done > 0 {
		parts = append(parts, "built "+strconv.FormatInt(builds.Done, 10))
	}
	if s.Copies.Done > 0 {
		parts = append(parts, "fetched "+strconv.FormatInt(s.Copies.Done, 10)+s.fetchedFrom())
	}
	text := "Finished at " + now.Format(time.TimeOnly) + " after " + elapsed
	if len(parts) > 0 {
		text += ": " + strings.Join(parts, ", ")
	}
	return []string{st.Paint(ic.With(ic.Done, text), ui.Green)}
}

func (s *State) fetchedFrom() string {
	fetched := s.Fetched()
	if len(fetched) == 0 {
		return ""
	}
	sources := make([]string, len(fetched))
	for i, f := range fetched {
		sources[i] = ui.Bytes(f.Bytes) + " from " + f.Substituter
	}
	return " (" + strings.Join(sources, ", ") + ")"
}

func failureReport(st ui.Style, failed []*Derivation) []string {
	ic := st.Icons()
	var lines []string
	for i, d := range failed {
		corner, indent := "├─ ", "│  "
		if i == len(failed)-1 {
			corner, indent = "╰─ ", "   "
		}
		head := st.Paint(ic.With(ic.Failed, d.Name), ui.Bold, ui.Red)
		if d.Failure.Reason != "" {
			head += st.Paint(": "+d.Failure.Reason, ui.Red)
		}
		lines = append(lines, st.Paint(corner, ui.Gray)+head)
		for _, log := range d.Failure.Log {
			lines = append(lines, st.Paint(indent+"│ ", ui.Gray)+st.Paint(log, ui.Faint))
		}
		lines = append(lines, st.Paint(indent, ui.Gray)+st.Paint("full log: ", ui.Faint)+st.Paint("nix log "+d.Path, ui.Cyan))
	}
	return lines
}

func (s *State) withStatus(status Status) []*Derivation {
	var out []*Derivation
	for _, d := range s.drvs {
		if d.Status == status {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, compareDerivations)
	return out
}

type branch struct {
	ancestors []bool
	last      bool
}

func (b branch) child(last bool) branch {
	return branch{ancestors: append(slices.Clip(b.ancestors), !b.last), last: last}
}

func connectors(st ui.Style, b branch) string {
	var out strings.Builder
	for depth, more := range b.ancestors {
		if more || depth == 0 {
			out.WriteString("│  ")
			continue
		}
		out.WriteString("   ")
	}
	if !b.last || len(b.ancestors) == 0 {
		out.WriteString("├─ ")
	} else {
		out.WriteString("╰─ ")
	}
	return st.Paint(out.String(), ui.Gray)
}

func (s *State) tree(st ui.Style, now time.Time) []string {
	roots, children := s.forest()
	var rows []string
	var walk func(path string, b branch)
	walk = func(path string, b branch) {
		rows = append(rows, connectors(st, b)+s.drvRow(st, s.drvs[path], now))
		kids := children[path]
		if chain := s.waitingChain(kids, children); len(chain) > minCollapsedChain {
			rows = append(rows, connectors(st, b.child(false))+st.Paint(fmt.Sprintf("… %d more", len(chain)-1), ui.Gray))
			kids = chain[len(chain)-1:]
		}
		for i, kid := range kids {
			walk(kid, b.child(i == len(kids)-1))
		}
	}
	for _, root := range roots {
		walk(root, branch{})
	}
	return append(rows, s.transferRows(st, now)...)
}

func (s *State) waitingChain(kids []string, children map[string][]string) []string {
	var chain []string
	for len(kids) == 1 && s.drvs[kids[0]].Status == Planned && len(children[kids[0]]) == 1 {
		chain = append(chain, kids[0])
		kids = children[kids[0]]
	}
	return chain
}

func (s *State) fitRows(st ui.Style, now time.Time, budget int) []string {
	if budget == 0 {
		return nil
	}
	var rows []string
	for _, d := range s.active() {
		rows = append(rows, connectors(st, branch{})+s.drvRow(st, d, now))
	}
	rows = append(rows, s.transferRows(st, now)...)
	if len(rows) <= budget {
		return rows
	}
	hidden := len(rows) - budget + 1
	return append(rows[:budget-1], connectors(st, branch{})+st.Paint(fmt.Sprintf("… and %d more", hidden), ui.Gray))
}

func (s *State) active() []*Derivation {
	var out []*Derivation
	for _, d := range s.drvs {
		if d.Status == Running || d.Status == Failed {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, compareDerivations)
	return out
}

func (s *State) forest() ([]string, map[string][]string) {
	depths := make(map[string]int)
	parents := make(map[string]string)
	children := make(map[string][]*Derivation)
	var roots []*Derivation
	for _, d := range s.active() {
		for node := d; node != nil; {
			if _, seen := parents[node.Path]; seen {
				break
			}
			parent := s.parentOf(node.Path, depths)
			parents[node.Path] = parent
			if parent == "" {
				roots = append(roots, node)
				break
			}
			children[parent] = append(children[parent], node)
			node = s.drvs[parent]
		}
	}
	slices.SortFunc(roots, compareDerivations)
	out := make(map[string][]string, len(children))
	for parent, kids := range children {
		slices.SortFunc(kids, compareDerivations)
		paths := make([]string, len(kids))
		for i, k := range kids {
			paths[i] = k.Path
		}
		out[parent] = paths
	}
	rootPaths := make([]string, len(roots))
	for i, r := range roots {
		rootPaths[i] = r.Path
	}
	return rootPaths, out
}

func (s *State) waitingConsumers(path string) []string {
	var out []string
	for _, c := range s.consumers[path] {
		if d, ok := s.drvs[c]; ok && d.Status == Planned {
			out = append(out, c)
		}
	}
	return out
}

func (s *State) parentOf(path string, depths map[string]int) string {
	best, bestDepth := "", -1
	for _, c := range s.waitingConsumers(path) {
		depth := s.depth(c, depths)
		if depth > bestDepth || (depth == bestDepth && c < best) {
			best, bestDepth = c, depth
		}
	}
	return best
}

func (s *State) depth(path string, memo map[string]int) int {
	if d, ok := memo[path]; ok {
		return d
	}
	depth := 0
	for _, c := range s.waitingConsumers(path) {
		depth = max(depth, s.depth(c, memo)+1)
	}
	memo[path] = depth
	return depth
}

func statusRank(s Status) int {
	switch s {
	case Failed:
		return 0
	case Running:
		return 1
	}
	return 2
}

func compareDerivations(a, b *Derivation) int {
	return cmp.Or(
		cmp.Compare(statusRank(a.Status), statusRank(b.Status)),
		cmp.Compare(a.Name, b.Name),
		cmp.Compare(a.Path, b.Path),
	)
}

func (s *State) drvRow(st ui.Style, d *Derivation, now time.Time) string {
	ic := st.Icons()
	switch d.Status {
	case Running:
		row := st.Paint(ic.Running+ic.Gap, ui.Yellow) + d.Name
		if d.Phase != "" {
			row += "  " + st.Paint(d.Phase, ui.Gray)
		}
		timer := formatDuration(now.Sub(d.Start))
		if d.Estimate > 0 {
			timer += " / " + formatDuration(d.Estimate)
		}
		return row + "  " + st.Paint(ic.With(ic.Clock, timer), ui.Gray)
	case Failed:
		return st.Paint(ic.With(ic.Failed, d.Name), ui.Red)
	}
	return st.Paint(ic.Planned+ic.Gap, ui.Blue) + d.Name
}

func (s *State) transferRows(st ui.Style, now time.Time) []string {
	transfers := make([]*Transfer, 0, len(s.transfers))
	nameWidth := 0
	for _, t := range s.transfers {
		transfers = append(transfers, t)
		nameWidth = max(nameWidth, ui.Width(t.Name))
	}
	nameWidth = min(nameWidth, maxTransferName)
	slices.SortFunc(transfers, func(a, b *Transfer) int {
		return cmp.Or(a.Start.Compare(b.Start), cmp.Compare(a.Name, b.Name))
	})
	ic := st.Icons()
	rows := make([]string, len(transfers))
	for i, t := range transfers {
		row := connectors(st, branch{}) + st.Paint(ic.Download+ic.Gap, ui.Cyan) + ui.PadRight(t.Name, nameWidth)
		if progress := transferProgress(st, t, now); progress != "" {
			row += "  " + progress
		}
		rows[i] = row + "  " + st.Paint(t.Substituter, ui.Faint)
	}
	return rows
}

func transferProgress(st ui.Style, t *Transfer, now time.Time) string {
	if t.Unpacking() {
		return bar(st, t.Unpack.Done, t.Unpack.Expected, ui.Blue) + "  " + st.Paint("unpacking "+ui.ByteProgress(t.Unpack.Done, t.Unpack.Expected), ui.Gray)
	}
	if t.Download.Expected <= 0 {
		return ""
	}
	done := t.Downloaded()
	out := bar(st, done, t.Download.Expected, ui.Cyan) + "  " + st.Paint(ui.ByteProgress(done, t.Download.Expected), ui.Gray)
	if elapsed := now.Sub(t.Start); elapsed >= time.Second {
		out += "  " + st.Paint(ui.Bytes(int64(float64(done)/elapsed.Seconds()))+"/s", ui.Cyan)
	}
	return out
}

func bar(st ui.Style, done, total int64, color ui.Attr) string {
	filled := int(min(done, total) * barWidth / total)
	return st.Paint(strings.Repeat("━", filled), color) + st.Paint(strings.Repeat("─", barWidth-filled), ui.Gray)
}

func (s *State) footer(st ui.Style, now time.Time) []footerLine {
	var lines []footerLine
	if row, ok := s.downloadRow(st, now); ok {
		lines = append(lines, footerLine{joined: "┝━ ", alone: "┍━ ", body: row})
	}
	ic := st.Icons()
	clock := st.Paint(ic.With(ic.Clock, formatDuration(now.Sub(s.started))), ui.Gray)
	var trailer []string
	if phase := s.Phase(); phase != PhaseNone {
		trailer = append(trailer, st.Paint(phase.String(), ui.Magenta, ui.Italic))
	}
	trailer = append(trailer, clock)
	sep := st.Paint(" │ ", ui.Gray)
	columns := s.columns(st)
	if len(columns) == 0 {
		return append(lines, footerLine{joined: "┕━ ", alone: "╺━ ", body: strings.Join(trailer, sep)})
	}
	headers := make([]string, len(columns))
	cells := make([]string, len(columns))
	for i, c := range columns {
		width := max(ui.Width(c.header), ui.Width(c.cell))
		headers[i] = st.Paint(c.header, ui.Bold)
		if i < len(columns)-1 {
			headers[i] = ui.PadRight(headers[i], width)
		}
		cells[i] = ui.PadRight(c.cell, width)
	}
	return append(lines,
		footerLine{joined: "┝━━━ ", alone: "┍━━━ ", body: strings.Join(headers, sep)},
		footerLine{joined: "┕━ ", alone: "╺━ ", body: st.Paint(ic.Summary+ic.Gap, ui.Bold) + strings.Join(append(cells, trailer...), sep)},
	)
}

type column struct {
	header string
	cell   string
}

func (s *State) columns(st ui.Style) []column {
	ic := st.Icons()
	count := func(icon string, n int64, color ui.Attr) string {
		return st.Paint(ic.With(icon, strconv.FormatInt(n, 10)), color)
	}
	var out []column
	if b := s.Builds(); b.Any() {
		cell := []string{count(ic.Running, b.Running, ui.Yellow), count(ic.Done, b.Done, ui.Green)}
		if b.Failed > 0 {
			cell = append(cell, count(ic.Failed, b.Failed, ui.Red))
		}
		cell = append(cell, count(ic.Planned, b.Planned, ui.Blue))
		out = append(out, column{header: "Builds", cell: strings.Join(cell, " ")})
	}
	if c := s.Copies; c.Expected > 0 {
		cell := []string{count(ic.Download, c.Running, ui.Yellow), count(ic.Done, c.Done, ui.Green)}
		if c.Failed > 0 {
			cell = append(cell, count(ic.Failed, c.Failed, ui.Red))
		}
		cell = append(cell, count(ic.Planned, c.Waiting(), ui.Blue))
		if fetched := s.Downloads(); fetched.Done > 0 {
			cell = append(cell, st.Paint(ui.Bytes(fetched.Done), ui.Cyan))
		}
		out = append(out, column{header: "Downloads", cell: strings.Join(cell, " ")})
	}
	return out
}

func (s *State) downloadRow(st ui.Style, now time.Time) (string, bool) {
	d := s.Downloads()
	if d.Expected <= 0 || s.Copies.Done >= s.Copies.Expected {
		return "", false
	}
	ic := st.Icons()
	row := st.Paint(ic.Download+ic.Gap, ui.Cyan) + bar(st, d.Done, d.Expected, ui.Cyan) + "  " + st.Paint(ui.ByteProgress(d.Done, d.Expected), ui.Gray)
	rate := s.rate.perSecond(now)
	if rate <= 0 {
		return row, true
	}
	row += "  " + st.Paint(ui.Bytes(rate)+"/s", ui.Cyan)
	if eta := time.Duration((d.Expected-d.Done)/rate) * time.Second; eta >= time.Second {
		row += "  " + st.Paint("ETA "+formatDuration(eta), ui.Gray)
	}
	return row, true
}

func formatDuration(d time.Duration) string {
	secs := int64(max(d, 0) / time.Second)
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh%dm", secs/3600, secs%3600/60)
}
