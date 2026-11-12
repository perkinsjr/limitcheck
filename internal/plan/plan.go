// Package plan builds a sealed list of request slots with absolute fire times.
package plan

import (
	"fmt"
	"sort"
	"time"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Slot is one planned request. FireAt is absolute so the scheduler can wait
// until that instant instead of sleeping in a fixed loop.
type Slot struct {
	ID       int
	FireAt   time.Time
	Origin   int
	Identity int
	Tag      spec.Tag
}

// Plan is the sealed campaign. Safety and the dry run both read this value.
type Plan struct {
	Slots       []Slot
	Limit       spec.Limit
	BoundaryAt  time.Time
	WindowStart time.Time
}

const minWindow = 400 * time.Millisecond

// Build returns the slots for c as of now. The same now produces the same plan.
//
// Window edges are unix-time multiples of the limit window. Fixed-window
// limiters usually reset on that grid, so the boundary bursts can sit on
// either side of a real edge. A limiter that resets on some other clock is
// reported as not tested, rather than as a pass.
func Build(c spec.Campaign, now time.Time) (Plan, error) {
	if c.Limit.Count < 1 || c.Limit.Window <= 0 {
		return Plan{}, fmt.Errorf("limit must be positive")
	}
	if c.Limit.Window < minWindow {
		return Plan{}, fmt.Errorf("window %s is shorter than %s", c.Limit.Window, minWindow)
	}
	if len(c.Identities) == 0 {
		return Plan{}, fmt.Errorf("campaign has no identity")
	}
	if len(c.Origins) == 0 {
		return Plan{}, fmt.Errorf("campaign has no origin")
	}
	if len(c.Checks) == 0 {
		return Plan{}, fmt.Errorf("campaign has no checks")
	}

	w := c.Limit.Window
	n := c.Limit.Count
	has := selected(c)

	needBurst := has[spec.CheckBurst] || has[spec.CheckIsolation] || has[spec.CheckRecovery] || has[spec.CheckHeaders] || has[spec.CheckMultiInstance]
	needIso := has[spec.CheckIsolation] && len(c.Identities) > 1
	needRecovery := has[spec.CheckRecovery]
	needBoundary := has[spec.CheckBoundary]
	needSteady := has[spec.CheckAccuracy] || has[spec.CheckIsolation] || has[spec.CheckHeaders]

	var slots []Slot
	nextID := 0
	add := func(at time.Time, origin, identity int, tag spec.Tag, count int) {
		for i := 0; i < count; i++ {
			slots = append(slots, Slot{
				ID:       nextID,
				FireAt:   at,
				Origin:   origin,
				Identity: identity,
				Tag:      tag,
			})
			nextID++
		}
	}
	addSpaced := func(at time.Time, gap time.Duration, origin, identity int, tag spec.Tag, count int) {
		for i := 0; i < count; i++ {
			slots = append(slots, Slot{
				ID:       nextID,
				FireAt:   at.Add(time.Duration(i) * gap),
				Origin:   origin,
				Identity: identity,
				Tag:      tag,
			})
			nextID++
		}
	}

	// Land the first window on the epoch grid, a little in the future, so a
	// late timer still fires inside that window.
	cursor := nextBoundary(now.Add(200*time.Millisecond), w)
	windowStart := cursor
	var boundaryAt time.Time

	if needBurst || needIso || needRecovery {
		if needBurst {
			origins := 1
			if has[spec.CheckMultiInstance] {
				origins = len(c.Origins)
			}
			burstAt := cursor.Add(burstOffset(w))
			for o := 0; o < origins; o++ {
				add(burstAt, o, 0, spec.TagBurst, n+burstExtra)
			}
		}
		if needIso {
			add(cursor.Add(isolationOffset(w)), 0, 1, spec.TagProbe, 1)
		}
		if needRecovery {
			add(cursor.Add(earlyProbeOffset(w)), 0, 0, spec.TagProbe, 1)
		}
		cursor = cursor.Add(w)
	}

	if needRecovery {
		add(cursor.Add(lateProbeOffset(w)), 0, 0, spec.TagProbe, 1)
		cursor = cursor.Add(w)
	}

	if needBoundary {
		boundaryAt = cursor.Add(w)
		add(boundaryAt.Add(-preLead(w)), 0, 0, spec.TagPreBoundary, n)
		add(boundaryAt.Add(postLag(w)), 0, 0, spec.TagPostBoundary, n)
		cursor = boundaryAt.Add(w)
	}

	if needSteady {
		extra := n / 5
		if extra < 2 {
			extra = 2
		}
		count := n + extra
		off := steadyOffset(w)
		available := w - off - 20*time.Millisecond
		if available < time.Millisecond {
			available = time.Millisecond
		}
		gap := 20 * time.Millisecond
		if count > 1 && time.Duration(count-1)*gap > available {
			gap = available / time.Duration(count-1)
			if gap < time.Millisecond {
				gap = time.Millisecond
			}
		}
		addSpaced(cursor.Add(off), gap, 0, 0, spec.TagSteady, count)
	}

	if len(slots) == 0 {
		return Plan{}, fmt.Errorf("checks produced no requests")
	}

	sort.Slice(slots, func(i, j int) bool {
		if slots[i].FireAt.Equal(slots[j].FireAt) {
			return slots[i].ID < slots[j].ID
		}
		return slots[i].FireAt.Before(slots[j].FireAt)
	})

	return Plan{
		Slots:       slots,
		Limit:       c.Limit,
		BoundaryAt:  boundaryAt,
		WindowStart: windowStart,
	}, nil
}

// burstExtra is the number of requests past the limit in a concurrent burst.
const burstExtra = 2

func selected(c spec.Campaign) map[spec.CheckKind]bool {
	m := make(map[spec.CheckKind]bool, len(c.Checks))
	for _, k := range c.Checks {
		m[k] = true
	}
	return m
}

func nextBoundary(t time.Time, w time.Duration) time.Time {
	wn := int64(w)
	u := t.UnixNano()
	if rem := u % wn; rem != 0 {
		u += wn - rem
	}
	return time.Unix(0, u)
}

func clamp(d, low, high time.Duration) time.Duration {
	if d < low {
		return low
	}
	if d > high {
		return high
	}
	return d
}

func burstOffset(w time.Duration) time.Duration {
	return clamp(w*3/100, 15*time.Millisecond, 200*time.Millisecond)
}

func isolationOffset(w time.Duration) time.Duration {
	return clamp(w*15/100, 40*time.Millisecond, 400*time.Millisecond)
}

func earlyProbeOffset(w time.Duration) time.Duration {
	return clamp(w*45/100, 80*time.Millisecond, 2*time.Second)
}

func lateProbeOffset(w time.Duration) time.Duration {
	return clamp(w*20/100, 80*time.Millisecond, time.Second)
}

func preLead(w time.Duration) time.Duration {
	return clamp(w/5, 80*time.Millisecond, 500*time.Millisecond)
}

func postLag(w time.Duration) time.Duration {
	return clamp(w/20, 30*time.Millisecond, 150*time.Millisecond)
}

func steadyOffset(w time.Duration) time.Duration {
	return clamp(w*8/100, 20*time.Millisecond, 500*time.Millisecond)
}
