package unit

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errInfinity = errors.New("infinity")

var spanUnits = []struct {
	names []string
	unit  time.Duration
}{
	{[]string{"usec", "us", "µs", "μs"}, time.Microsecond},
	{[]string{"msec", "ms"}, time.Millisecond},
	{[]string{"seconds", "second", "sec", "s"}, time.Second},
	{[]string{"minutes", "minute", "min", "m"}, time.Minute},
	{[]string{"hours", "hour", "hr", "h"}, time.Hour},
	{[]string{"days", "day", "d"}, 24 * time.Hour},
	{[]string{"weeks", "week", "w"}, 7 * 24 * time.Hour},
	{[]string{"months", "month", "M"}, time.Duration(30.44 * float64(24*time.Hour))},
	{[]string{"years", "year", "y"}, time.Duration(365.25 * float64(24*time.Hour))},
}

func lookupSpanUnit(name string) (time.Duration, bool) {
	for _, u := range spanUnits {
		if slices.Contains(u.names, name) {
			return u.unit, true
		}
	}
	return 0, false
}

func parseSpan(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "infinity" {
		return 0, errInfinity
	}
	if s == "" {
		return 0, errors.New("empty time span")
	}
	var total float64
	rest := s
	for rest != "" {
		rest = strings.TrimLeft(rest, " \t")
		numEnd := strings.IndexFunc(rest, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
		if numEnd == 0 {
			return 0, fmt.Errorf("time span %q: expected a number at %q", s, rest)
		}
		if numEnd < 0 {
			numEnd = len(rest)
		}
		value, err := strconv.ParseFloat(rest[:numEnd], 64)
		if err != nil {
			return 0, fmt.Errorf("time span %q: %w", s, err)
		}
		rest = strings.TrimLeft(rest[numEnd:], " \t")
		unitEnd := strings.IndexFunc(rest, func(r rune) bool { return (r >= '0' && r <= '9') || r == ' ' || r == '\t' || r == '.' })
		if unitEnd < 0 {
			unitEnd = len(rest)
		}
		unit := time.Second
		if name := rest[:unitEnd]; name != "" {
			u, ok := lookupSpanUnit(name)
			if !ok {
				return 0, fmt.Errorf("time span %q: unknown unit %q", s, name)
			}
			unit = u
		}
		total += value * float64(unit)
		rest = rest[unitEnd:]
	}
	if total > math.MaxInt64 {
		return 0, fmt.Errorf("time span %q overflows", s)
	}
	return time.Duration(total), nil
}

func parseTimeout(s string) (time.Duration, error) {
	d, err := parseSpan(s)
	if errors.Is(err, errInfinity) {
		return 0, nil
	}
	return d, err
}
