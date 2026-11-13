// Package sched turns a check's waves into clock times just before they fire.
package sched

import (
	"fmt"
	"time"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Wave is one immutable burst of requests. A check returns these and does not keep them.
type Wave struct {
	Name     string
	Marker   string
	Count    int
	InFlight int
	Interval time.Duration
	Identity int
	Fanout   bool
	Gate     spec.Gate
	Anchor   string
	// Offset is added to the anchor. Negative means before a window boundary.
	Offset time.Duration
	// At is a sealed list of fire times. Recovery uses it for the one follower.
	At       []time.Time
	Follower bool
}

type Stimulus struct {
	Waves []Wave
}

type Slot struct {
	GridAt   time.Time
	NotAfter time.Time
	// Edge is the window boundary a boundary burst is measured against.
	Edge     time.Time
	Origin   int
	Identity int
	Marker   string
	Wave     string
	Index    int
}

type Planned struct {
	Wave  Wave
	Slots []Slot
	Held  bool
}

func Cost(waves []Wave, origins int) int {
	if origins < 1 {
		origins = 1
	}
	n := 0
	for _, w := range waves {
		c := w.Count
		if len(w.At) > 0 {
			c = len(w.At)
		}
		if w.Fanout && origins > 1 {
			c *= origins
		}
		n += c
	}
	return n
}

func WithinCaps(cost int, caps spec.Caps) error {
	if caps.MaxRequests < 1 {
		return fmt.Errorf("max requests must be at least 1")
	}
	if cost < 1 {
		return fmt.Errorf("the checks produced no requests")
	}
	if cost > caps.MaxRequests {
		return fmt.Errorf("plan sends %d requests, above the max of %d", cost, caps.MaxRequests)
	}
	return nil
}

// Admit refuses a real send to a host that is not literal loopback.
// Dry-run does not call this. There is no DNS lookup.
func Admit(in spec.Input) error {
	if in.Yes {
		return nil
	}
	for _, o := range in.Origins {
		if !loopback(o.Host) {
			return fmt.Errorf("%s is not a local address, so pass --yes to send traffic there", hostOnly(o.Host))
		}
	}
	return nil
}

// Stamp gives one wave its grid times from now. Call it immediately before the wave fires.
func Stamp(w Wave, now time.Time, window time.Duration, origins int) Planned {
	if len(w.At) > 0 {
		slots := make([]Slot, 0, len(w.At))
		for i, at := range w.At {
			slots = append(slots, Slot{
				GridAt: at, Identity: w.Identity, Marker: mark(w), Wave: w.Name, Index: i,
			})
		}
		return Planned{Wave: w, Slots: spread(w, slots, origins)}
	}
	base := now.Add(25 * time.Millisecond)
	switch w.Gate.Kind {
	case spec.Ready:
		base = NextBoundary(now.Add(window).Add(30*time.Millisecond), window).Add(w.Offset)
	case spec.Immediate, spec.AfterPrevious, spec.AfterLast, spec.WhenIdle, spec.RetryAfter:
		base = now.Add(25 * time.Millisecond).Add(w.Offset)
	case spec.SnapshotReset:
		return StampEdge([]Wave{w}, now, window, origins)[0]
	}
	ww := w
	ww.Offset = 0
	slots := spread(w, expand(ww, base), origins)
	if w.Gate.MustHit {
		for _, s := range slots {
			if !s.GridAt.After(now) {
				return Planned{Wave: w, Held: true}
			}
		}
	}
	return Planned{Wave: w, Slots: slots}
}

// StampEdge stamps every wave that shares an anchor onto one window boundary.
// If the earlier side would already be past, the whole group is held.
func StampEdge(group []Wave, now time.Time, window time.Duration, origins int) []Planned {
	minOff := group[0].Offset
	for _, w := range group[1:] {
		if w.Offset < minOff {
			minOff = w.Offset
		}
	}
	need := now.Add(window).Add(40 * time.Millisecond).Add(-minOff)
	boundary := NextBoundary(need, window)
	if !boundary.Add(minOff).After(now) {
		held := make([]Planned, len(group))
		for i, w := range group {
			held[i] = Planned{Wave: w, Held: true}
		}
		return held
	}
	out := make([]Planned, 0, len(group))
	for _, w := range group {
		ww := w
		ww.Offset = 0
		slots := expand(ww, boundary.Add(w.Offset))
		for i := range slots {
			slots[i].Edge = boundary
			if w.Offset < 0 {
				slots[i].NotAfter = boundary
			}
		}
		out = append(out, Planned{Wave: w, Slots: spread(w, slots, origins)})
	}
	return out
}

// NextBoundary is the next unix-time multiple of window at or after t.
func NextBoundary(t time.Time, window time.Duration) time.Time {
	if window <= 0 {
		return t
	}
	u := t.UnixNano()
	w := int64(window)
	if u%w == 0 {
		return t
	}
	return time.Unix(0, (u/w+1)*w)
}

func expand(w Wave, base time.Time) []Slot {
	n := w.Count
	if n < 1 {
		n = 1
	}
	gap := w.Interval
	if w.InFlight >= n || gap == 0 {
		gap = 0
	}
	slots := make([]Slot, n)
	for i := 0; i < n; i++ {
		slots[i] = Slot{
			GridAt:   base.Add(time.Duration(i) * gap),
			Identity: w.Identity,
			Marker:   mark(w),
			Wave:     w.Name,
			Index:    i,
		}
	}
	return slots
}

func spread(w Wave, slots []Slot, origins int) []Slot {
	if !w.Fanout || origins < 2 {
		for i := range slots {
			slots[i].Origin = 0
		}
		return slots
	}
	out := make([]Slot, 0, len(slots)*origins)
	for _, s := range slots {
		for o := 0; o < origins; o++ {
			s.Origin = o
			out = append(out, s)
		}
	}
	return out
}

func mark(w Wave) string {
	if w.Marker != "" {
		return w.Marker
	}
	return w.Name
}

func hostOnly(host string) string {
	if h, _, ok := splitHost(host); ok {
		return h
	}
	return host
}

func loopback(host string) bool {
	h, _, _ := splitHost(host)
	h = trimBrackets(h)
	if h == "localhost" || len(h) > len(".localhost") && h[len(h)-len(".localhost"):] == ".localhost" {
		return true
	}
	ip := parseIP(h)
	return ip != nil && ip.IsLoopback()
}
