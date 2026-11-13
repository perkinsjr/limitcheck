package brokenserver

import (
	"sync"
	"time"
)

// The sleep in the racy limiter is the bug. Waiters all read the count, then
// all increment, so a burst larger than the limit still gets through.
const raceGap = 30 * time.Millisecond

// earlyGrant is long enough that the overlapping burst is still rejected,
// and short enough that a follow-up a moment later is allowed.
const earlyGrant = 20 * time.Millisecond

type limiter interface {
	allow(key string, now time.Time) (allowed bool, remaining int, reset time.Time)
}

type sliding struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	hits   map[string][]time.Time
}

func newSliding(n int, window time.Duration) *sliding {
	return &sliding{n: n, window: window, hits: map[string][]time.Time{}}
}

func (s *sliding) allow(key string, now time.Time) (bool, int, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := now.Add(-s.window)
	kept := s.hits[key][:0]
	for _, t := range s.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	reset := now.Add(s.window)
	if len(kept) > 0 {
		reset = kept[0].Add(s.window)
	}
	if len(kept) >= s.n {
		s.hits[key] = kept
		return false, 0, reset
	}
	kept = append(kept, now)
	s.hits[key] = kept
	return true, s.n - len(kept), reset
}

type fixed struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	bucket map[string]int64
	count  map[string]int
}

func newFixed(n int, window time.Duration) *fixed {
	return &fixed{n: n, window: window, bucket: map[string]int64{}, count: map[string]int{}}
}

func (f *fixed) allow(key string, now time.Time) (bool, int, time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := now.UnixNano() / int64(f.window)
	if f.bucket[key] != b {
		f.bucket[key] = b
		f.count[key] = 0
	}
	reset := time.Unix(0, (b+1)*int64(f.window))
	if f.count[key] >= f.n {
		return false, 0, reset
	}
	f.count[key]++
	return true, f.n - f.count[key], reset
}

type raceLim struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	count  map[string]int
	start  map[string]time.Time
}

func newRace(n int, window time.Duration) *raceLim {
	return &raceLim{n: n, window: window, count: map[string]int{}, start: map[string]time.Time{}}
}

func (r *raceLim) allow(key string, now time.Time) (bool, int, time.Time) {
	r.mu.Lock()
	start, ok := r.start[key]
	if !ok || now.Sub(start) >= r.window {
		r.start[key] = now
		r.count[key] = 0
		start = now
	}
	seen := r.count[key]
	r.mu.Unlock()
	time.Sleep(raceGap)
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.start[key]) >= r.window {
		r.start[key] = now
		r.count[key] = 0
	}
	reset := r.start[key].Add(r.window)
	if seen >= r.n {
		return false, 0, reset
	}
	r.count[key]++
	return true, r.n - r.count[key], reset
}

type early struct {
	inner  *sliding
	mu     sync.Mutex
	denied map[string]time.Time
	gifted map[string]bool
}

func newEarly(n int, window time.Duration) *early {
	return &early{inner: newSliding(n, window), denied: map[string]time.Time{}, gifted: map[string]bool{}}
}

func (e *early) allow(key string, now time.Time) (bool, int, time.Time) {
	ok, rem, reset := e.inner.allow(key, now)
	if ok {
		return true, rem, reset
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, seen := e.denied[key]
	if !seen {
		e.denied[key] = now
		return false, 0, reset
	}
	if !e.gifted[key] && now.Sub(t) >= earlyGrant {
		e.gifted[key] = true
		return true, 0, reset
	}
	return false, 0, reset
}

type openLim struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	used   int
}

func newOpen(n int, window time.Duration) *openLim {
	return &openLim{n: n, window: window}
}

func (o *openLim) allow(string, time.Time) (bool, int, time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.used++
	rem := o.n - o.used
	if rem < 0 {
		rem = 0
	}
	return true, rem, time.Now().Add(o.window)
}
