package supervisor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stubbedev/phix/internal/atomicfile"
)

const maxTimerSleep = time.Minute

type timerRun struct {
	activated     time.Time
	lastTrigger   time.Time
	persisted     time.Time
	randomization time.Duration
	next          time.Time
	wake          *time.Timer
	gen           uint64
}

func (m *Manager) stampPath(st *state) string {
	return filepath.Join(m.layout.StampDir(), st.name().String())
}

func (m *Manager) readStamp(st *state) time.Time {
	data, err := os.ReadFile(m.stampPath(st))
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}
	}
	if err != nil {
		m.ulog(st, "Reading the persistent timer stamp: %v", err)
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		m.ulog(st, "Ignoring the corrupt persistent timer stamp %s: %v", m.stampPath(st), err)
		return time.Time{}
	}
	return t
}

func (m *Manager) writeStamp(st *state, t time.Time) {
	err := atomicfile.Replace(m.stampPath(st), 0o600, func(w io.Writer) error {
		_, err := fmt.Fprintln(w, t.UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		m.ulog(st, "Writing the persistent timer stamp: %v", err)
	}
}

func (m *Manager) randomization(st *state) time.Duration {
	t := st.u.Timer
	if t.FixedRandomDelay {
		return fixedDelay(st.name().String(), t.RandomizedDelay, m.layout.Host.Hostname, m.layout.Host.UID)
	}
	return m.random(t.RandomizedDelay)
}

func (m *Manager) startTimer(st *state) {
	st.timer = &timerRun{
		activated:     m.now(),
		persisted:     m.readStamp(st),
		randomization: m.randomization(st),
	}
	m.setState(st, stateActive, "waiting")
	m.unitActivated(st)
	m.schedule(st)
}

func (m *Manager) stopTimer(st *state) {
	if run := st.timer; run != nil {
		if run.wake != nil {
			run.wake.Stop()
		}
		st.timer = nil
	}
	if st.running() {
		m.setState(st, stateInactive, "dead")
		m.unitDeactivated(st)
	}
}

func (m *Manager) schedule(st *state) {
	run := st.timer
	if run == nil {
		return
	}
	t := st.u.Timer
	if run.wake != nil {
		run.wake.Stop()
		run.wake = nil
	}
	in := scheduleInputs{
		started:       m.started,
		activated:     run.activated,
		lastTrigger:   run.lastTrigger,
		persisted:     run.persisted,
		randomization: run.randomization,
	}
	if target := m.states[t.Unit]; target != nil {
		in.unitActive, in.unitInactive = target.activeEnter, target.inactiveEnter
	}
	next, ok := nextElapse(t, in)
	if !ok {
		run.next = time.Time{}
		if t.RemainAfterElapse || len(t.OnUnitActive)+len(t.OnUnitInactive) > 0 {
			m.setState(st, stateActive, "elapsed")
			return
		}
		m.stopTimer(st)
		return
	}
	run.next = next
	m.setState(st, stateActive, "waiting")
	delay := min(max(next.Sub(m.now()), 0), maxTimerSleep)
	run.gen++
	gen := run.gen
	run.wake = time.AfterFunc(delay, func() {
		m.post(func() {
			if st.timer == run && run.gen == gen {
				m.timerWake(st)
			}
		})
	})
}

func (m *Manager) timerWake(st *state) {
	run := st.timer
	now := m.now()
	if now.Before(run.next) {
		m.schedule(st)
		return
	}
	t := st.u.Timer
	run.lastTrigger = now
	if t.Persistent {
		m.writeStamp(st, now)
		run.persisted = now
	}
	if !t.FixedRandomDelay {
		run.randomization = m.randomization(st)
	}
	m.ulog(st, "Triggering %s", t.Unit)
	if !m.shuttingDown {
		m.enqueueStart(t.Unit, func(err error) {
			if err != nil {
				m.ulog(st, "Triggering %s failed: %v", t.Unit, err)
			}
		})
	}
	m.schedule(st)
}
