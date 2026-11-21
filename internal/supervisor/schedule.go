package supervisor

import (
	"hash/fnv"
	"strconv"
	"time"

	"github.com/stubbedev/phix/internal/unit"
)

type scheduleInputs struct {
	started       time.Time
	activated     time.Time
	lastTrigger   time.Time
	persisted     time.Time
	unitActive    time.Time
	unitInactive  time.Time
	randomization time.Duration
}

func nextElapse(t *unit.TimerConfig, in scheduleInputs) (time.Time, bool) {
	var (
		best  time.Time
		found bool
	)
	consider := func(c time.Time) {
		if !found || c.Before(best) {
			best, found = c, true
		}
	}
	monotonic := func(base time.Time, spans []time.Duration) {
		if base.IsZero() {
			return
		}
		for _, d := range spans {
			c := base.Add(d)
			if !in.lastTrigger.IsZero() && !c.After(in.lastTrigger) {
				continue
			}
			consider(c)
		}
	}
	monotonic(in.activated, t.OnActive)
	monotonic(in.started, t.OnBoot)
	monotonic(in.started, t.OnStartup)
	monotonic(in.unitActive, t.OnUnitActive)
	monotonic(in.unitInactive, t.OnUnitInactive)
	calBase := in.activated
	switch {
	case !in.lastTrigger.IsZero():
		calBase = in.lastTrigger
	case t.Persistent && !in.persisted.IsZero():
		calBase = in.persisted
	}
	for _, cal := range t.OnCalendar {
		if c, ok := cal.Next(calBase); ok {
			consider(c)
		}
	}
	if !found {
		return time.Time{}, false
	}
	return best.Add(in.randomization), true
}

func fixedDelay(name string, limit time.Duration, host string, uid int) time.Duration {
	if limit <= 0 {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write([]byte(host))
	h.Write([]byte(strconv.Itoa(uid)))
	return time.Duration(h.Sum64() % uint64(limit)) //nolint:gosec
}
