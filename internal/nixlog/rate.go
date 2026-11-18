package nixlog

import "time"

const (
	rateWindow     = 5 * time.Second
	sampleInterval = 250 * time.Millisecond
)

type sample struct {
	at    time.Time
	bytes int64
}

type throughput struct {
	samples []sample
}

func (t *throughput) record(at time.Time, bytes int64) {
	if n := len(t.samples); n > 1 && at.Sub(t.samples[n-2].at) < sampleInterval {
		t.samples[n-1] = sample{at: at, bytes: bytes}
		return
	}
	t.samples = append(t.samples, sample{at: at, bytes: bytes})
	drop := 0
	for drop < len(t.samples)-1 && at.Sub(t.samples[drop+1].at) >= rateWindow {
		drop++
	}
	t.samples = append(t.samples[:0], t.samples[drop:]...)
}

func (t *throughput) perSecond(now time.Time) int64 {
	if len(t.samples) < 2 {
		return 0
	}
	first, last := t.samples[0], t.samples[len(t.samples)-1]
	if now.Sub(last.at) >= rateWindow {
		return 0
	}
	elapsed := now.Sub(first.at)
	if elapsed <= 0 {
		return 0
	}
	return int64(float64(last.bytes-first.bytes) / elapsed.Seconds())
}
