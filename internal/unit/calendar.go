package unit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	minYear = 1970
	maxYear = 2199
)

var calendarShorthands = map[string]string{
	"minutely":     "*-*-* *:*:00",
	"hourly":       "*-*-* *:00:00",
	"daily":        "*-*-* 00:00:00",
	"weekly":       "Mon *-*-* 00:00:00",
	"monthly":      "*-*-01 00:00:00",
	"quarterly":    "*-01,04,07,10-01 00:00:00",
	"semiannually": "*-01,07-01 00:00:00",
	"yearly":       "*-01-01 00:00:00",
	"annually":     "*-01-01 00:00:00",
}

var weekdayNames = []struct {
	short, long string
	day         time.Weekday
}{
	{"Mon", "Monday", time.Monday},
	{"Tue", "Tuesday", time.Tuesday},
	{"Wed", "Wednesday", time.Wednesday},
	{"Thu", "Thursday", time.Thursday},
	{"Fri", "Friday", time.Friday},
	{"Sat", "Saturday", time.Saturday},
	{"Sun", "Sunday", time.Sunday},
}

type valueSet struct {
	min     int
	allowed []bool
}

func fullSet(lo, hi int) valueSet {
	s := valueSet{min: lo, allowed: make([]bool, hi-lo+1)}
	for i := range s.allowed {
		s.allowed[i] = true
	}
	return s
}

func singleSet(lo, hi, v int) valueSet {
	s := valueSet{min: lo, allowed: make([]bool, hi-lo+1)}
	s.allowed[v-lo] = true
	return s
}

func (s valueSet) has(v int) bool {
	i := v - s.min
	return i >= 0 && i < len(s.allowed) && s.allowed[i]
}

func (s valueSet) max() int { return s.min + len(s.allowed) - 1 }

type Calendar struct {
	spec     string
	weekdays [7]bool
	years    valueSet
	months   valueSet
	days     valueSet
	fromEnd  bool
	hours    valueSet
	minutes  valueSet
	seconds  valueSet
	location *time.Location
}

func (c Calendar) String() string { return c.spec }

func ParseCalendar(spec string, local *time.Location) (Calendar, error) {
	c, err := parseCalendar(spec, local)
	if err != nil {
		return Calendar{}, fmt.Errorf("calendar spec %q: %w", spec, err)
	}
	if _, ok := c.Next(time.Date(minYear, 1, 1, 0, 0, 0, 0, time.UTC)); !ok {
		return Calendar{}, fmt.Errorf("calendar spec %q never elapses", spec)
	}
	return c, nil
}

func parseCalendar(spec string, local *time.Location) (Calendar, error) {
	c := Calendar{
		spec:     spec,
		years:    fullSet(minYear, maxYear),
		months:   fullSet(1, 12),
		days:     fullSet(1, 31),
		hours:    singleSet(0, 23, 0),
		minutes:  singleSet(0, 59, 0),
		seconds:  singleSet(0, 59, 0),
		location: local,
	}
	for i := range c.weekdays {
		c.weekdays[i] = true
	}
	tokens := strings.Fields(spec)
	if len(tokens) == 0 {
		return Calendar{}, errors.New("empty")
	}
	if loc, ok := timezoneToken(tokens[len(tokens)-1]); ok && len(tokens) > 1 {
		c.location = loc
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 1 {
		if expanded, ok := calendarShorthands[strings.ToLower(tokens[0])]; ok {
			tokens = strings.Fields(expanded)
		}
	}
	if len(tokens) > 0 && !strings.ContainsAny(tokens[0], "-:~*0123456789") {
		weekdays, err := parseWeekdays(tokens[0])
		if err != nil {
			return Calendar{}, err
		}
		c.weekdays = weekdays
		tokens = tokens[1:]
	}
	if len(tokens) > 0 && strings.ContainsAny(tokens[0], "-~") {
		if err := c.parseDate(tokens[0]); err != nil {
			return Calendar{}, err
		}
		tokens = tokens[1:]
	}
	if len(tokens) > 0 && strings.Contains(tokens[0], ":") {
		if err := c.parseTime(tokens[0]); err != nil {
			return Calendar{}, err
		}
		tokens = tokens[1:]
	}
	if len(tokens) > 0 {
		return Calendar{}, fmt.Errorf("unexpected %q", strings.Join(tokens, " "))
	}
	return c, nil
}

func timezoneToken(tok string) (*time.Location, bool) {
	if strings.ContainsAny(tok, ":*~,") || strings.Contains(tok, "..") {
		return nil, false
	}
	if _, err := parseWeekdays(tok); err == nil {
		return nil, false
	}
	loc, err := time.LoadLocation(tok)
	if err != nil || tok == "Local" {
		return nil, false
	}
	return loc, true
}

func parseWeekdays(tok string) ([7]bool, error) {
	var out [7]bool
	for item := range strings.SplitSeq(tok, ",") {
		lo, hi, isRange := strings.Cut(item, "..")
		if !isRange {
			lo, hi, isRange = strings.Cut(item, "-")
		}
		from, err := weekdayIndex(lo)
		if err != nil {
			return out, err
		}
		to := from
		if isRange {
			if to, err = weekdayIndex(hi); err != nil {
				return out, err
			}
		}
		for i := from; ; i = (i + 1) % 7 {
			out[weekdayNames[i].day] = true
			if i == to {
				break
			}
		}
	}
	return out, nil
}

func weekdayIndex(name string) (int, error) {
	for i, w := range weekdayNames {
		if strings.EqualFold(name, w.short) || strings.EqualFold(name, w.long) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%q is not a weekday", name)
}

func (c *Calendar) parseDate(tok string) error {
	head, dayPart, fromEnd := strings.Cut(tok, "~")
	c.fromEnd = fromEnd
	parts := strings.Split(head, "-")
	if !c.fromEnd {
		dayPart = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	switch len(parts) {
	case 1:
		parts = []string{"*", parts[0]}
	case 2:
	default:
		return fmt.Errorf("date %q: want [YEAR-]MONTH-DAY or [YEAR-]MONTH~DAY", tok)
	}
	var err error
	if c.years, err = parseComponent(parts[0], minYear, maxYear); err != nil {
		return fmt.Errorf("year: %w", err)
	}
	if c.months, err = parseComponent(parts[1], 1, 12); err != nil {
		return fmt.Errorf("month: %w", err)
	}
	if c.days, err = parseComponent(dayPart, 1, 31); err != nil {
		return fmt.Errorf("day: %w", err)
	}
	return nil
}

func (c *Calendar) parseTime(tok string) error {
	parts := strings.Split(tok, ":")
	if len(parts) == 2 {
		parts = append(parts, "00")
	}
	if len(parts) != 3 {
		return fmt.Errorf("time %q: want HOUR:MINUTE[:SECOND]", tok)
	}
	if strings.Contains(parts[2], ".") {
		return fmt.Errorf("time %q: fractional seconds are not supported", tok)
	}
	var err error
	if c.hours, err = parseComponent(parts[0], 0, 23); err != nil {
		return fmt.Errorf("hour: %w", err)
	}
	if c.minutes, err = parseComponent(parts[1], 0, 59); err != nil {
		return fmt.Errorf("minute: %w", err)
	}
	if c.seconds, err = parseComponent(parts[2], 0, 59); err != nil {
		return fmt.Errorf("second: %w", err)
	}
	return nil
}

func parseComponent(s string, lo, hi int) (valueSet, error) {
	set := valueSet{min: lo, allowed: make([]bool, hi-lo+1)}
	for item := range strings.SplitSeq(s, ",") {
		base, stepText, hasStep := strings.Cut(item, "/")
		step := 0
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n <= 0 {
				return valueSet{}, fmt.Errorf("%q: repetition %q is not a positive integer", item, stepText)
			}
			step = n
		}
		from, to := lo, hi
		switch fromText, toText, isRange := strings.Cut(base, ".."); {
		case base == "*":
		case isRange:
			var err error
			if from, err = componentValue(fromText, lo, hi); err != nil {
				return valueSet{}, err
			}
			if to, err = componentValue(toText, lo, hi); err != nil {
				return valueSet{}, err
			}
			if to < from {
				return valueSet{}, fmt.Errorf("range %q runs backwards", base)
			}
		default:
			v, err := componentValue(base, lo, hi)
			if err != nil {
				return valueSet{}, err
			}
			from = v
			if !hasStep {
				to = v
			}
		}
		if step == 0 {
			step = 1
		}
		for v := from; v <= to; v += step {
			set.allowed[v-lo] = true
		}
	}
	return set, nil
}

func componentValue(s string, lo, hi int) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("%d is outside %d..%d", v, lo, hi)
	}
	return v, nil
}

func (c Calendar) dayMatches(t time.Time) bool {
	if !c.weekdays[t.Weekday()] {
		return false
	}
	day := t.Day()
	if !c.fromEnd {
		return c.days.has(day)
	}
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return c.days.has(last - day + 1)
}

func (c Calendar) Next(after time.Time) (time.Time, bool) {
	loc := c.location
	t := after.In(loc).Truncate(time.Second).Add(time.Second)
	at := func(y int, mo time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, mo, d, h, mi, s, 0, loc)
	}
	for t.Year() <= c.years.max() {
		prev := t
		y, mo, d := t.Date()
		h, mi, s := t.Clock()
		switch {
		case !c.years.has(y):
			if y < c.years.min {
				t = at(c.years.min, 1, 1, 0, 0, 0)
			} else {
				t = at(y+1, 1, 1, 0, 0, 0)
			}
		case !c.months.has(int(mo)):
			t = at(y, mo+1, 1, 0, 0, 0)
		case !c.dayMatches(t):
			t = at(y, mo, d+1, 0, 0, 0)
		case !c.hours.has(h):
			t = at(y, mo, d, h+1, 0, 0)
		case !c.minutes.has(mi):
			t = at(y, mo, d, h, mi+1, 0)
		case !c.seconds.has(s):
			t = at(y, mo, d, h, mi, s+1)
		default:
			return t, true
		}
		if !t.After(prev) {
			t = prev.Add(time.Second)
		}
	}
	return time.Time{}, false
}
