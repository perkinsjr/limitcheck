package testserver

import (
	"sync"
	"time"
)

// limiter decides one request. remaining is the count left after the decision.
// reset is when the server claims the window opens again.
type limiter interface {
	Take(key string, now time.Time) (allowed bool, remaining int, reset time.Time)
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

func (s *sliding) Take(key string, now time.Time) (bool, int, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := prune(s.hits[key], now, s.window)
	reset := resetAt(h, now, s.window)
	if len(h) >= s.n {
		s.hits[key] = h
		return false, 0, reset
	}
	h = append(h, now)
	s.hits[key] = h
	return true, s.n - len(h), resetAt(h, now, s.window)
}

func prune(hits []time.Time, now time.Time, window time.Duration) []time.Time {
	cut := now.Add(-window)
	i := 0
	for i < len(hits) && !hits[i].After(cut) {
		i++
	}
	return hits[i:]
}

func resetAt(hits []time.Time, now time.Time, window time.Duration) time.Time {
	if len(hits) == 0 {
		return now.Add(window)
	}
	return hits[0].Add(window)
}

// fixedWindow resets on unix-time edges, so a full limit just before the edge
// and another full limit just after it both succeed.
type fixedWindow struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	counts map[string]map[int64]int
}

func newFixed(n int, window time.Duration) *fixedWindow {
	return &fixedWindow{n: n, window: window, counts: map[string]map[int64]int{}}
}

func (f *fixedWindow) Take(key string, now time.Time) (bool, int, time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wn := int64(f.window)
	idx := now.UnixNano() / wn
	reset := time.Unix(0, (idx+1)*wn)
	buckets := f.counts[key]
	if buckets == nil {
		buckets = map[int64]int{}
		f.counts[key] = buckets
	}
	if buckets[idx] >= f.n {
		return false, 0, reset
	}
	buckets[idx]++
	return true, f.n - buckets[idx], reset
}

// racy reads the counter, waits, then increments. Waiters that read together
// all pass the check. The sleep is the gap a check-then-increment race needs
// in order to show up every time, not only when the scheduler happens to interleave.
type racy struct {
	inner *sliding
}

func newRacy(n int, window time.Duration) *racy {
	return &racy{inner: newSliding(n, window)}
}

func (r *racy) Take(key string, now time.Time) (bool, int, time.Time) {
	r.inner.mu.Lock()
	h := prune(r.inner.hits[key], now, r.inner.window)
	snapshot := len(h)
	reset := resetAt(h, now, r.inner.window)
	r.inner.hits[key] = h
	r.inner.mu.Unlock()

	if snapshot >= r.inner.n {
		return false, 0, reset
	}
	time.Sleep(30 * time.Millisecond)

	r.inner.mu.Lock()
	defer r.inner.mu.Unlock()
	h = append(r.inner.hits[key], now)
	r.inner.hits[key] = h
	rem := r.inner.n - len(h)
	if rem < 0 {
		rem = 0
	}
	return true, rem, resetAt(h, now, r.inner.window)
}

// early enforces the window, then lets one later request through while still
// telling the client the reset is at the real window edge. The 50ms floor keeps
// the concurrent over-limit burst rejected. The grant is the single request
// that arrives after that.
type early struct {
	inner    *sliding
	mu       sync.Mutex
	deniedAt map[string]time.Time
	granted  map[string]bool
}

func newEarly(n int, window time.Duration) *early {
	return &early{
		inner:    newSliding(n, window),
		deniedAt: map[string]time.Time{},
		granted:  map[string]bool{},
	}
}

func (e *early) Take(key string, now time.Time) (bool, int, time.Time) {
	ok, rem, reset := e.inner.Take(key, now)
	if ok {
		return true, rem, reset
	}
	e.mu.Lock()
	if e.deniedAt[key].IsZero() {
		e.deniedAt[key] = now
	}
	grant := !e.granted[key] && now.Sub(e.deniedAt[key]) >= 50*time.Millisecond
	if grant {
		e.granted[key] = true
	}
	e.mu.Unlock()
	if !grant {
		return false, 0, reset
	}
	e.inner.mu.Lock()
	e.inner.hits[key] = append(e.inner.hits[key], now)
	e.inner.mu.Unlock()
	return true, 0, reset
}

// open accepts every request and still reports a decreasing remainder.
type open struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	seq    map[string]int
}

func newOpen(n int, window time.Duration) *open {
	return &open{n: n, window: window, seq: map[string]int{}}
}

func (o *open) Take(key string, now time.Time) (bool, int, time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq[key]++
	rem := o.n - o.seq[key]
	if rem < 0 {
		rem = 0
	}
	return true, rem, now.Add(o.window)
}

func newLimiter(mode string, n int, window time.Duration) limiter {
	switch mode {
	case ModeFixed:
		return newFixed(n, window)
	case ModeRacy:
		return newRacy(n, window)
	case ModeEarly:
		return newEarly(n, window)
	case ModeOpen:
		return newOpen(n, window)
	default:
		return newSliding(n, window)
	}
}
