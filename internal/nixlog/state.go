package nixlog

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/phix/internal/storepath"
	"github.com/stubbedev/phix/internal/ui"
)

type Progress struct {
	Done     int64
	Expected int64
	Running  int64
	Failed   int64
}

func (p Progress) Waiting() int64 {
	return max(p.Expected-p.Done-p.Running-p.Failed, 0)
}

type Status int

const (
	Planned Status = iota + 1
	Running
	Done
	Failed
	Cancelled
)

type Derivation struct {
	Path     string
	Name     string
	Status   Status
	Phase    string
	Start    time.Time
	Estimate time.Duration
	Failure  Failure
	inputs   []string
}

type Failure struct {
	Reason string
	Log    []string
}

type BuildCounts struct {
	Running int64
	Done    int64
	Failed  int64
	Planned int64
}

func (c BuildCounts) Any() bool {
	return c.Running+c.Done+c.Failed+c.Planned > 0
}

type ByteCount struct {
	Done     int64
	Expected int64
}

type Transfer struct {
	Name        string
	Substituter string
	Start       time.Time
	Download    ByteCount
	Unpack      ByteCount
	downloading int
}

func (t *Transfer) Unpacking() bool {
	return t.downloading == 0 && t.Unpack.Expected > 0
}

func (t *Transfer) Downloaded() int64 {
	d, u := t.Download, t.Unpack
	if t.downloading == 0 || d.Expected <= 0 || u.Expected <= 0 {
		return d.Done
	}
	streamed := int64(float64(min(u.Done, u.Expected)) / float64(u.Expected) * float64(d.Expected))
	return max(d.Done, streamed)
}

type Downloads struct {
	Done     int64
	Expected int64
	Unpacked int64
}

type SubstituterBytes struct {
	Substituter string
	Bytes       int64
}

type Phase int

const (
	PhaseNone Phase = iota
	PhaseQuerying
	PhaseEvaluating
)

func (p Phase) String() string {
	switch p {
	case PhaseEvaluating:
		return "evaluating"
	case PhaseQuerying:
		return "querying caches"
	}
	return ""
}

var phasePrefixes = []struct {
	prefix string
	phase  Phase
}{
	{"evaluating ", PhaseEvaluating},
	{"querying info about missing paths", PhaseQuerying},
}

func phaseOf(text string) Phase {
	for _, p := range phasePrefixes {
		if strings.HasPrefix(text, p.prefix) {
			return p.phase
		}
	}
	return PhaseNone
}

type Line struct {
	Prefix string
	Text   string
}

func (l Line) String() string {
	if l.Prefix == "" {
		return l.Text
	}
	return l.Prefix + "> " + l.Text
}

type activity struct {
	typ    ActivityType
	parent uint64
	name   string
	drv    string
	phase  Phase
}

type expectation struct {
	activity uint64
	typ      ActivityType
}

type nar struct {
	transfer    uint64
	substituter string
	bytes       int64
}

type planKind int

const (
	planNone planKind = iota
	planBuild
	planFetch
)

var (
	buildPlanHeader = regexp.MustCompile(`^(?:these \d+ derivations|this derivation) will be built:$`)
	fetchPlanHeader = regexp.MustCompile(`^(?:these \d+ paths|this path) will be fetched`)
	failedBuild     = regexp.MustCompile(`(?:Cannot build|builder for) '(/nix/store/[^']+\.drv)'`)
	failureReason   = regexp.MustCompile(`(?m)^\s*Reason: (.*?)\.?$`)
	dependencyCause = regexp.MustCompile(`^\d+ dependenc(?:y|ies) failed$`)
	failureLogLine  = regexp.MustCompile(`(?m)^\s*> ?(.*)$`)
)

type Options struct {
	BuildLogs bool
	Live      bool
	Inputs    func(drvPath string) ([]string, error)
	Estimates map[string]time.Duration
}

type State struct {
	opts       Options
	started    time.Time
	activities map[uint64]*activity
	drvs       map[string]*Derivation
	consumers  map[string][]string
	transfers  map[uint64]*Transfer
	measured   map[string]time.Duration
	expected   map[expectation]int64
	nars       map[uint64]*nar
	fetched    map[string]int64
	phases     map[Phase]int
	rate       throughput
	plan       planKind
	Copies     Progress
}

func NewState(opts Options, started time.Time) *State {
	return &State{
		opts:       opts,
		started:    started,
		activities: make(map[uint64]*activity),
		drvs:       make(map[string]*Derivation),
		consumers:  make(map[string][]string),
		transfers:  make(map[uint64]*Transfer),
		measured:   make(map[string]time.Duration),
		expected:   make(map[expectation]int64),
		nars:       make(map[uint64]*nar),
		fetched:    make(map[string]int64),
		phases:     make(map[Phase]int),
	}
}

func (s *State) Apply(e Event, now time.Time) ([]Line, error) {
	switch e.Action {
	case ActionStart:
		return s.start(e, now)
	case ActionStop:
		s.stop(e.ID, now)
	case ActionResult:
		return s.result(e, now), nil
	case ActionMsg:
		return s.msg(e)
	}
	return nil, nil
}

func (s *State) msg(e Event) ([]Line, error) {
	text := ui.Strip(e.Msg)
	if m := failedBuild.FindStringSubmatch(text); m != nil {
		if d, ok := s.drvs[m[1]]; ok {
			s.recordFailure(d, text)
			if s.opts.Live {
				return nil, nil
			}
		}
	}
	if e.Level > LevelInfo {
		return []Line{{Text: e.Msg}}, nil
	}
	switch {
	case buildPlanHeader.MatchString(text):
		s.plan = planBuild
	case fetchPlanHeader.MatchString(text):
		s.plan = planFetch
	case s.plan != planNone && strings.HasPrefix(text, "  /nix/store/"):
		if s.plan == planBuild {
			if _, err := s.derivation(strings.TrimSpace(text)); err != nil {
				return nil, err
			}
		}
	default:
		s.plan = planNone
		return []Line{{Text: e.Msg}}, nil
	}
	if s.opts.Live {
		return nil, nil
	}
	return []Line{{Text: e.Msg}}, nil
}

func (s *State) recordFailure(d *Derivation, text string) {
	reason := ""
	if m := failureReason.FindStringSubmatch(text); m != nil {
		reason = m[1]
	}
	if dependencyCause.MatchString(reason) {
		d.Status = Cancelled
		return
	}
	d.Status = Failed
	d.Failure = Failure{Reason: reason}
	for _, m := range failureLogLine.FindAllStringSubmatch(text, -1) {
		d.Failure.Log = append(d.Failure.Log, m[1])
	}
}

func (s *State) derivation(drvPath string) (*Derivation, error) {
	if d, ok := s.drvs[drvPath]; ok {
		return d, nil
	}
	var inputs []string
	if s.opts.Inputs != nil {
		var err error
		if inputs, err = s.opts.Inputs(drvPath); err != nil {
			return nil, err
		}
	}
	d := &Derivation{
		Path:     drvPath,
		Name:     storepath.Name(drvPath),
		Status:   Planned,
		Estimate: s.opts.Estimates[storepath.Package(drvPath)],
		inputs:   inputs,
	}
	s.drvs[drvPath] = d
	for _, in := range inputs {
		s.consumers[in] = append(s.consumers[in], drvPath)
	}
	return d, nil
}

func (s *State) start(e Event, now time.Time) ([]Line, error) {
	typ := ActivityType(e.Type)
	a := &activity{typ: typ, parent: e.Parent, name: activityName(e), phase: phaseOf(e.Text)}
	s.activities[e.ID] = a
	if a.phase != PhaseNone {
		s.phases[a.phase]++
	}
	switch typ {
	case ActivityBuild:
		drvPath, _ := e.StringField(0)
		d, err := s.derivation(drvPath)
		if err != nil {
			return nil, err
		}
		a.drv = drvPath
		d.Status, d.Start, d.Phase = Running, now, ""
	case ActivityCopyPath:
		from, _ := e.StringField(1)
		s.transfers[e.ID] = &Transfer{Name: a.name, Substituter: substituterName(from), Start: now}
	case ActivityFileTransfer:
		if t, id := s.transferOf(a); t != nil {
			t.downloading++
			s.nars[e.ID] = &nar{transfer: id, substituter: t.Substituter}
		}
	}
	if s.opts.Live || e.Text == "" || e.Level > LevelInfo {
		return nil, nil
	}
	return []Line{{Text: e.Text}}, nil
}

func (s *State) stop(id uint64, now time.Time) {
	a, ok := s.activities[id]
	if !ok {
		return
	}
	delete(s.activities, id)
	delete(s.transfers, id)
	if a.phase != PhaseNone {
		s.phases[a.phase]--
	}
	if n, ok := s.nars[id]; ok {
		s.fetched[n.substituter] += n.bytes
		delete(s.nars, id)
		if t, ok := s.transfers[n.transfer]; ok {
			t.downloading--
		}
	}
	if a.drv == "" {
		return
	}
	if d := s.drvs[a.drv]; d.Status == Running {
		d.Status = Done
		s.measured[storepath.Package(d.Path)] = now.Sub(d.Start)
	}
}

func (s *State) result(e Event, now time.Time) []Line {
	a, ok := s.activities[e.ID]
	if !ok {
		return nil
	}
	switch ResultType(e.Type) {
	case ResultProgress:
		s.progress(e, a, now)
	case ResultSetExpected:
		typ, okType := e.IntField(0)
		n, okN := e.IntField(1)
		if okType && okN {
			s.expected[expectation{activity: e.ID, typ: ActivityType(typ)}] = n
		}
	case ResultSetPhase:
		if phase, ok := e.StringField(0); ok && a.drv != "" {
			s.drvs[a.drv].Phase = phase
		}
	case ResultBuildLogLine, ResultPostBuildLogLine:
		line, ok := e.StringField(0)
		if !ok || !s.opts.BuildLogs {
			return nil
		}
		return []Line{{Prefix: a.name, Text: line}}
	}
	return nil
}

func (s *State) progress(e Event, a *activity, now time.Time) {
	p := Progress{}
	p.Done, _ = e.IntField(0)
	p.Expected, _ = e.IntField(1)
	p.Running, _ = e.IntField(2)
	p.Failed, _ = e.IntField(3)
	bytes := ByteCount{Done: p.Done, Expected: p.Expected}
	switch a.typ {
	case ActivityCopyPaths:
		s.Copies = p
	case ActivityCopyPath:
		t, ok := s.transfers[e.ID]
		if !ok {
			return
		}
		t.Unpack = bytes
		s.rate.record(now, s.Downloads().Done)
	case ActivityFileTransfer:
		n, ok := s.nars[e.ID]
		if !ok {
			return
		}
		n.bytes = p.Done
		if t, ok := s.transfers[n.transfer]; ok {
			t.Download = bytes
		}
		s.rate.record(now, s.Downloads().Done)
	}
}

func (s *State) transferOf(a *activity) (*Transfer, uint64) {
	for id := a.parent; id != 0; {
		if t, ok := s.transfers[id]; ok {
			return t, id
		}
		parent, ok := s.activities[id]
		if !ok {
			return nil, 0
		}
		id = parent.parent
	}
	return nil, 0
}

func (s *State) Measured() map[string]time.Duration { return s.measured }

func (s *State) Builds() BuildCounts {
	var c BuildCounts
	for _, d := range s.drvs {
		switch d.Status {
		case Running:
			c.Running++
		case Done:
			c.Done++
		case Failed:
			c.Failed++
		case Planned:
			c.Planned++
		}
	}
	return c
}

func (s *State) Downloads() Downloads {
	var d Downloads
	for _, n := range s.fetched {
		d.Done += n
	}
	for _, t := range s.transfers {
		if t.downloading > 0 {
			d.Done += t.Downloaded()
		}
	}
	for k, n := range s.expected {
		switch k.typ {
		case ActivityFileTransfer:
			d.Expected += n
		case ActivityCopyPath:
			d.Unpacked += n
		}
	}
	return d
}

func (s *State) Fetched() []SubstituterBytes {
	totals := make(map[string]int64, len(s.fetched))
	for name, n := range s.fetched {
		totals[name] += n
	}
	for _, t := range s.transfers {
		if t.downloading > 0 {
			totals[t.Substituter] += t.Downloaded()
		}
	}
	out := make([]SubstituterBytes, 0, len(totals))
	for name, n := range totals {
		if n > 0 {
			out = append(out, SubstituterBytes{Substituter: name, Bytes: n})
		}
	}
	slices.SortFunc(out, func(a, b SubstituterBytes) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Substituter, b.Substituter))
	})
	return out
}

func (s *State) Phase() Phase {
	for p := PhaseEvaluating; p > PhaseNone; p-- {
		if s.phases[p] > 0 {
			return p
		}
	}
	return PhaseNone
}

func substituterName(uri string) string {
	if _, rest, ok := strings.Cut(uri, "://"); ok {
		uri = rest
	}
	return strings.TrimSuffix(uri, "/")
}

func activityName(e Event) string {
	p, ok := e.StringField(0)
	if !ok {
		return ""
	}
	return storepath.Name(p)
}
