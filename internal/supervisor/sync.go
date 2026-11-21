package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	sdunit "github.com/coreos/go-systemd/v22/unit"

	"github.com/stubbedev/phix/internal/unit"
)

func wanted(set *unit.Set) map[unit.Name]bool {
	out := map[unit.Name]bool{}
	root, err := unit.ParseName(unit.DefaultTarget)
	if err != nil {
		panic(err)
	}
	queue := []unit.Name{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		u := set.Units[n]
		if out[n] || u == nil {
			continue
		}
		out[n] = true
		queue = append(queue, slices.Concat(u.Deps.Wants, u.Deps.Requires, u.Deps.BindsTo)...)
	}
	return out
}

func planSync(old, next *unit.Set, active func(unit.Name) bool) []Change {
	oldWanted, newWanted := wanted(old), wanted(next)
	names := map[unit.Name]bool{}
	for _, s := range []*unit.Set{old, next} {
		for n := range s.Units {
			names[n] = true
		}
	}
	var changes []Change
	for _, n := range sortedNames(names) {
		ou, nu := old.Units[n], next.Units[n]
		running := active(n)
		changed := ou != nil && nu != nil && ou.Digest != nu.Digest
		var action Action
		switch {
		case running && nu == nil:
			action = ActionStop
		case running && changed:
			switch nu.SwitchMethod {
			case unit.SwitchKeepOld:
				continue
			case unit.SwitchReload:
				action = ActionReload
			default:
				action = ActionRestart
			}
		case !running && nu != nil && newWanted[n] && (ou == nil || changed || !oldWanted[n]):
			action = ActionStart
		default:
			continue
		}
		changes = append(changes, Change{Action: action, Unit: n.String()})
	}
	return changes
}

func (m *Manager) sync(done func(changes []Change, failures []string, err error)) {
	next, err := unit.Load(m.layout.Host, m.local)
	if err != nil {
		done(nil, nil, err)
		return
	}
	env, err := m.loadEnvironment()
	if err != nil {
		done(nil, nil, err)
		return
	}
	changes := planSync(m.set, next, func(n unit.Name) bool {
		st := m.states[n]
		return st != nil && (st.running() || st.restart != nil)
	})
	m.env = env
	m.adopt(next)
	for _, n := range sortedNames(next.Errors) {
		m.slog("%s: %v", n, next.Errors[n])
	}
	pending := len(changes)
	var failures []string
	finish := func(c Change) func(error) {
		return func(err error) {
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s %s: %v", c.Action, c.Unit, err))
			}
			pending--
			if pending == 0 {
				done(changes, failures, nil)
			}
		}
	}
	if pending == 0 {
		done(nil, nil, nil)
		return
	}
	for _, c := range changes {
		n, err := unit.ParseName(c.Unit)
		if err != nil {
			finish(c)(err)
			continue
		}
		switch c.Action {
		case ActionStop:
			m.enqueueStop(n, false, finish(c))
		case ActionRestart:
			m.enqueueStop(n, true, finish(c))
		case ActionReload:
			st := m.states[n]
			st.u, st.next = st.next, nil
			m.graph = buildGraph(m.unitMap())
			m.reload(st, finish(c))
		case ActionStart:
			m.enqueueStart(n, finish(c))
		}
	}
}

func applyManagerConfig(path string, env unit.Env, warn func(string, ...any)) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	opts, err := sdunit.DeserializeOptions(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, o := range opts {
		if o.Section != "Manager" || (o.Name != "DefaultEnvironment" && o.Name != "ManagerEnvironment") {
			warn("%s: ignoring [%s] %s=, it has no effect here", path, o.Section, o.Name)
			continue
		}
		vars, err := unit.ParseAssignmentList(o.Value)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, o.Name, err)
		}
		env.Apply(vars)
	}
	return nil
}
