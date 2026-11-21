package supervisor

import (
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/phix/internal/unit"
)

type activeState string

const (
	stateInactive     activeState = "inactive"
	stateActivating   activeState = "activating"
	stateActive       activeState = "active"
	stateDeactivating activeState = "deactivating"
	stateFailed       activeState = "failed"
	stateReloading    activeState = "reloading"
)

type jobKind int

const (
	jobStart jobKind = iota + 1
	jobStop
)

func (k jobKind) String() string {
	if k == jobStart {
		return "start"
	}
	return "stop"
}

type job struct {
	kind      jobKind
	thenStart bool
	running   bool
	waiters   []func(error)
}

type state struct {
	u             *unit.Unit
	next          *unit.Unit
	removed       bool
	active        activeState
	sub           string
	since         time.Time
	result        string
	stopResult    string
	main          *process
	control       *process
	onControl     func(exitStatus)
	onMain        func(exitStatus)
	onGone        func()
	goneProbe     bool
	groups        []int
	notify        *notifier
	ready         bool
	reloadWaiter  func(error)
	mainExit      *exitStatus
	statusText    string
	starts        []time.Time
	activeEnter   time.Time
	inactiveEnter time.Time
	env           unit.Env
	phase         *time.Timer
	phaseGen      uint64
	restart       *time.Timer
	timer         *timerRun
}

func (st *state) name() unit.Name { return st.u.Name }

func (st *state) running() bool {
	switch st.active {
	case stateActive, stateActivating, stateReloading, stateDeactivating:
		return true
	}
	return false
}

type graph struct {
	after       map[unit.Name][]unit.Name
	stopFirst   map[unit.Name][]unit.Name
	stopWith    map[unit.Name][]unit.Name
	boundBy     map[unit.Name][]unit.Name
	triggeredBy map[unit.Name][]unit.Name
}

func buildGraph(units map[unit.Name]*unit.Unit) graph {
	g := graph{
		after:       map[unit.Name][]unit.Name{},
		stopFirst:   map[unit.Name][]unit.Name{},
		stopWith:    map[unit.Name][]unit.Name{},
		boundBy:     map[unit.Name][]unit.Name{},
		triggeredBy: map[unit.Name][]unit.Name{},
	}
	add := func(m map[unit.Name][]unit.Name, k, v unit.Name) {
		if !slices.Contains(m[k], v) {
			m[k] = append(m[k], v)
		}
	}
	for _, n := range sortedNames(units) {
		u := units[n]
		for _, a := range u.Deps.After {
			add(g.after, n, a)
			add(g.stopFirst, a, n)
		}
		for _, b := range u.Deps.Before {
			add(g.after, b, n)
			add(g.stopFirst, n, b)
		}
		for _, d := range slices.Concat(u.Deps.Requires, u.Deps.BindsTo, u.Deps.PartOf) {
			add(g.stopWith, d, n)
		}
		for _, d := range u.Deps.BindsTo {
			add(g.boundBy, d, n)
		}
		if u.Timer != nil {
			add(g.triggeredBy, u.Timer.Unit, n)
		}
	}
	return g
}

func sortedNames[V any](m map[unit.Name]V) []unit.Name {
	names := slices.Collect(maps.Keys(m))
	slices.SortFunc(names, func(a, b unit.Name) int { return strings.Compare(a.String(), b.String()) })
	return names
}

type Manager struct {
	layout    Layout
	journal   *Journal
	helper    string
	inherited []string
	local     *time.Location
	now       func() time.Time
	started   time.Time
	random    func(time.Duration) time.Duration

	events chan func()
	done   chan struct{}

	env          unit.Env
	set          *unit.Set
	states       map[unit.Name]*state
	jobs         map[unit.Name]*job
	graph        graph
	shuttingDown bool
}

func newManager(layout Layout, helper string, inherited []string) *Manager {
	now := time.Now
	return &Manager{
		layout:    layout,
		journal:   NewJournal(layout.LogDir(), now),
		helper:    helper,
		inherited: inherited,
		local:     time.Local,
		now:       now,
		started:   now(),
		random: func(limit time.Duration) time.Duration {
			if limit <= 0 {
				return 0
			}
			return rand.N(limit) //nolint:gosec
		},
		events: make(chan func(), 64),
		done:   make(chan struct{}),
		states: map[unit.Name]*state{},
		jobs:   map[unit.Name]*job{},
	}
}

func (m *Manager) post(f func()) {
	select {
	case m.events <- f:
	case <-m.done:
	}
}

func (m *Manager) loop() {
	for f := range m.events {
		f()
		m.dispatch()
		if m.shuttingDown && m.idle() {
			close(m.done)
			return
		}
	}
}

func (m *Manager) idle() bool {
	if len(m.jobs) > 0 {
		return false
	}
	for _, st := range m.states {
		if st.main != nil || st.control != nil || st.onGone != nil {
			return false
		}
	}
	return true
}

func (m *Manager) logf(name string, format string, args ...any) {
	if err := m.journal.Append(name, fmt.Sprintf(format, args...)); err != nil {
		fmt.Fprintf(os.Stderr, "phix supervisor: %v\n", err)
	}
}

func (m *Manager) slog(format string, args ...any) { m.logf(SupervisorLog, format, args...) }

func (m *Manager) ulog(st *state, format string, args ...any) {
	m.logf(st.name().String(), format, args...)
}

func (m *Manager) reportErr(err error) {
	fmt.Fprintf(os.Stderr, "phix supervisor: %v\n", err)
}

func (m *Manager) setState(st *state, active activeState, sub string) {
	if st.active != active {
		st.since = m.now()
	}
	st.active, st.sub = active, sub
}

func (m *Manager) loadUnits() error {
	set, err := unit.Load(m.layout.Host, m.local)
	if err != nil {
		return err
	}
	env, err := m.loadEnvironment()
	if err != nil {
		return err
	}
	m.env = env
	m.adopt(set)
	for _, n := range sortedNames(set.Errors) {
		m.slog("%s: %v", n, set.Errors[n])
	}
	return nil
}

func (m *Manager) adopt(set *unit.Set) {
	m.set = set
	for n, u := range set.Units {
		st, ok := m.states[n]
		switch {
		case !ok:
			m.states[n] = &state{u: u, active: stateInactive, sub: "dead", since: m.now()}
		case st.running() && st.u.Digest != u.Digest:
			st.next, st.removed = u, false
		default:
			st.u, st.next, st.removed = u, nil, false
		}
	}
	for n, st := range m.states {
		if _, ok := set.Units[n]; ok {
			continue
		}
		if !st.running() && m.jobs[n] == nil {
			delete(m.states, n)
			continue
		}
		st.removed = true
	}
	units := make(map[unit.Name]*unit.Unit, len(m.states))
	for n, st := range m.states {
		units[n] = st.u
	}
	m.graph = buildGraph(units)
}

func (m *Manager) loadEnvironment() (unit.Env, error) {
	env := unit.EnvFrom(m.inherited)
	env["HOME"] = m.layout.Host.Home
	env["USER"] = m.layout.Host.User
	env["LOGNAME"] = m.layout.Host.User
	env["XDG_RUNTIME_DIR"] = m.layout.Host.RuntimeDir
	env["MANAGERPID"] = strconv.Itoa(os.Getpid())
	if err := applyManagerConfig(m.layout.Host.ManagerConfig(), env, m.slog); err != nil {
		return nil, err
	}
	dir := m.layout.Host.EnvironmentDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		path := dir + "/" + e.Name()
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if err := unit.ParseEnvironmentD(data, env); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	return env, nil
}

func (m *Manager) addJob(n unit.Name, kind jobKind, restart bool, waiter func(error)) {
	j := m.jobs[n]
	if j == nil {
		j = &job{kind: kind, thenStart: restart}
		m.jobs[n] = j
	} else {
		switch {
		case j.kind == kind:
			j.thenStart = j.thenStart || restart
		case j.kind == jobStart && kind == jobStop:
			for _, w := range j.waiters {
				w(fmt.Errorf("%s: the start job was replaced by a stop job", n))
			}
			j.waiters = nil
			j.kind, j.thenStart, j.running = jobStop, restart, false
		case j.kind == jobStop && kind == jobStart:
			j.thenStart = true
		}
	}
	if waiter != nil {
		j.waiters = append(j.waiters, waiter)
	}
}

func (m *Manager) finishJob(n unit.Name, err error) {
	j := m.jobs[n]
	if j == nil {
		return
	}
	if j.kind == jobStop && j.thenStart && err == nil {
		j.kind, j.thenStart, j.running = jobStart, false, false
		return
	}
	delete(m.jobs, n)
	for _, w := range j.waiters {
		w(err)
	}
	if err != nil && j.kind == jobStart {
		m.failDependents(n)
	}
	if st := m.states[n]; st != nil && st.removed && !st.running() && m.jobs[n] == nil {
		delete(m.states, n)
	}
}

func (m *Manager) failDependents(failed unit.Name) {
	for _, n := range sortedNames(m.jobs) {
		j := m.jobs[n]
		st := m.states[n]
		if j == nil || st == nil || j.running || j.kind != jobStart {
			continue
		}
		if slices.Contains(st.u.Deps.Requires, failed) || slices.Contains(st.u.Deps.BindsTo, failed) {
			m.finishJob(n, fmt.Errorf("%s: dependency %s failed", n, failed))
		}
	}
}

func (m *Manager) jobReady(n unit.Name, j *job) bool {
	if j.kind == jobStart {
		for _, v := range m.graph.after[n] {
			if jv := m.jobs[v]; jv != nil && jv != j && (jv.kind == jobStart || jv.thenStart) {
				return false
			}
		}
		return true
	}
	for _, v := range m.graph.stopFirst[n] {
		if jv := m.jobs[v]; jv != nil && jv != j && jv.kind == jobStop {
			return false
		}
	}
	return true
}

func (m *Manager) dispatch() {
	for {
		progressed := false
		for _, n := range sortedNames(m.jobs) {
			j := m.jobs[n]
			if j == nil || j.running || !m.jobReady(n, j) {
				continue
			}
			j.running, progressed = true, true
			m.runJob(n, j)
		}
		if !progressed {
			break
		}
	}
	if len(m.jobs) == 0 {
		return
	}
	for _, j := range m.jobs {
		if j.running {
			return
		}
	}
	names := sortedNames(m.jobs)
	m.slog("ordering cycle between the jobs of %s; running %s first", joinNames(names), names[0])
	j := m.jobs[names[0]]
	j.running = true
	m.runJob(names[0], j)
}

func joinNames(names []unit.Name) string {
	s := make([]string, len(names))
	for i, n := range names {
		s[i] = n.String()
	}
	return strings.Join(s, ", ")
}

func (m *Manager) runJob(n unit.Name, j *job) {
	st := m.states[n]
	if j.kind == jobStop {
		m.beginStop(st)
		return
	}
	if st.active == stateActive || st.active == stateReloading {
		m.finishJob(n, nil)
		return
	}
	for _, q := range st.u.Deps.Requisite {
		if qs := m.states[q]; qs == nil || qs.active != stateActive {
			m.finishJob(n, fmt.Errorf("%s: requisite %s is not active", n, q))
			return
		}
	}
	m.beginStart(st)
}

func (m *Manager) enqueueStart(root unit.Name, waiter func(error)) {
	visited := map[unit.Name]bool{}
	var visit func(n unit.Name, required bool) error
	visit = func(n unit.Name, required bool) error {
		if visited[n] {
			return nil
		}
		visited[n] = true
		st := m.states[n]
		if st == nil || st.removed {
			err := m.set.Errors[n]
			if err == nil {
				err = errors.New("unit is not loaded")
			}
			if required {
				return fmt.Errorf("%s: %w", n, err)
			}
			m.slog("not starting %s, wanted but not loadable: %v", n, err)
			return nil
		}
		for _, c := range st.u.Deps.Conflicts {
			if cs := m.states[c]; cs != nil && (cs.running() || m.jobs[c] != nil) {
				m.addJob(c, jobStop, false, nil)
			}
		}
		for _, r := range slices.Concat(st.u.Deps.Requires, st.u.Deps.BindsTo) {
			if err := visit(r, true); err != nil {
				if n == root {
					return err
				}
				return fmt.Errorf("%s: %w", n, err)
			}
		}
		for _, w := range st.u.Deps.Wants {
			if err := visit(w, false); err != nil {
				m.slog("not starting %s: %v", w, err)
			}
		}
		if n != root {
			m.addJob(n, jobStart, false, nil)
		}
		return nil
	}
	if err := visit(root, true); err != nil {
		if waiter != nil {
			waiter(err)
		}
		return
	}
	m.addJob(root, jobStart, false, waiter)
}

func (m *Manager) enqueueStop(root unit.Name, restart bool, waiter func(error)) {
	visited := map[unit.Name]bool{root: true}
	queue := []unit.Name{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, d := range m.graph.stopWith[n] {
			ds := m.states[d]
			if visited[d] || ds == nil || (!ds.running() && m.jobs[d] == nil && ds.restart == nil) {
				continue
			}
			visited[d] = true
			queue = append(queue, d)
			m.addJob(d, jobStop, restart && ds.running(), nil)
		}
	}
	m.addJob(root, jobStop, restart, waiter)
}

func (m *Manager) arm(st *state, d time.Duration, f func()) {
	m.disarm(st)
	if d <= 0 {
		return
	}
	st.phaseGen++
	gen := st.phaseGen
	st.phase = time.AfterFunc(d, func() {
		m.post(func() {
			if st.phaseGen == gen && st.phase != nil {
				st.phase = nil
				f()
			}
		})
	})
}

func (m *Manager) disarm(st *state) {
	if st.phase != nil {
		st.phase.Stop()
		st.phase = nil
	}
	st.phaseGen++
}

func (m *Manager) beginStart(st *state) {
	if st.restart != nil {
		st.restart.Stop()
		st.restart = nil
	}
	if st.next != nil {
		st.u, st.next = st.next, nil
		m.graph = buildGraph(m.unitMap())
	}
	st.result, st.stopResult, st.statusText = "", "", ""
	if ok, why := unit.CheckConditions(st.u.Conditions, m.env, m.layout.Host); !ok {
		m.ulog(st, "Skipped: condition %s is not met", why)
		st.result = "condition"
		m.setState(st, stateInactive, "dead")
		m.finishJob(st.name(), nil)
		return
	}
	if ok, why := unit.CheckConditions(st.u.Asserts, m.env, m.layout.Host); !ok {
		m.ulog(st, "Failed: assertion %s does not hold", why)
		st.result = "assert"
		m.setState(st, stateFailed, "failed")
		m.finishJob(st.name(), fmt.Errorf("%s: assertion %s does not hold", st.name(), why))
		return
	}
	if !m.withinStartLimit(st) {
		m.ulog(st, "Failed: started more than %d times within %s", st.u.StartLimit.Burst, st.u.StartLimit.Interval)
		st.result = "start-limit-hit"
		m.setState(st, stateFailed, "failed")
		m.finishJob(st.name(), fmt.Errorf("%s: start limit hit", st.name()))
		return
	}
	switch st.name().Kind() {
	case unit.Service:
		m.startService(st)
	case unit.Timer:
		m.startTimer(st)
		m.finishJob(st.name(), nil)
	default:
		m.setState(st, stateActive, "active")
		m.unitActivated(st)
		m.finishJob(st.name(), nil)
	}
}

func (m *Manager) unitMap() map[unit.Name]*unit.Unit {
	units := make(map[unit.Name]*unit.Unit, len(m.states))
	for n, st := range m.states {
		units[n] = st.u
	}
	return units
}

func (m *Manager) withinStartLimit(st *state) bool {
	lim := st.u.StartLimit
	now := m.now()
	if lim.Interval <= 0 || lim.Burst <= 0 {
		return true
	}
	kept := st.starts[:0]
	for _, t := range st.starts {
		if now.Sub(t) < lim.Interval {
			kept = append(kept, t)
		}
	}
	st.starts = kept
	if len(st.starts) >= lim.Burst {
		return false
	}
	st.starts = append(st.starts, now)
	return true
}

func (m *Manager) beginStop(st *state) {
	if st.restart != nil {
		st.restart.Stop()
		st.restart = nil
		if !st.running() || st.sub == "auto-restart" {
			m.setState(st, stateInactive, "dead")
		}
	}
	switch st.name().Kind() {
	case unit.Service:
		m.stopService(st)
	case unit.Timer:
		m.stopTimer(st)
		m.finishJob(st.name(), nil)
	default:
		if st.running() {
			m.setState(st, stateInactive, "dead")
			m.unitDeactivated(st)
		}
		m.finishJob(st.name(), nil)
	}
}

func (m *Manager) unitActivated(st *state) {
	st.activeEnter = m.now()
	for _, t := range m.graph.triggeredBy[st.name()] {
		if ts := m.states[t]; ts != nil {
			m.schedule(ts)
		}
	}
}

func (m *Manager) unitDeactivated(st *state) {
	st.inactiveEnter = m.now()
	for _, t := range m.graph.triggeredBy[st.name()] {
		if ts := m.states[t]; ts != nil {
			m.schedule(ts)
		}
	}
	if m.shuttingDown {
		return
	}
	for _, b := range m.graph.boundBy[st.name()] {
		if bs := m.states[b]; bs != nil && bs.running() {
			m.slog("stopping %s, it is bound to %s which stopped", b, st.name())
			m.enqueueStop(b, false, nil)
		}
	}
}

func (m *Manager) beginShutdown() {
	if m.shuttingDown {
		return
	}
	m.shuttingDown = true
	m.slog("shutting down")
	for _, n := range sortedNames(m.states) {
		st := m.states[n]
		if st.running() || st.restart != nil {
			m.addJob(n, jobStop, false, nil)
		}
	}
}
