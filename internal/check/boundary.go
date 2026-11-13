package check

import (
	"fmt"
	"math"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// BoundaryCheck straddles one window edge with two bursts.
func BoundaryCheck() Check {
	return Check{
		Name:             "boundary",
		IncludeByDefault: always,
		Stimulus:         boundaryStimulus,
		Verdict:          boundaryVerdict,
	}
}

func boundaryStimulus(in spec.Input) sched.Stimulus {
	n := in.Policy.Rate.Count
	w := in.Policy.Rate.Window
	return sched.Stimulus{Waves: []sched.Wave{
		{
			Name: "pre", Marker: "pre", Count: n, InFlight: n,
			Offset: -preLead(w), Anchor: "edge",
			Gate: spec.Gate{Kind: spec.SnapshotReset, MustHit: true},
		},
		{
			Name: "post", Marker: "post", Count: n, InFlight: n,
			Offset: postLag(w), Anchor: "edge",
			Gate: spec.Gate{Kind: spec.SnapshotReset, MustHit: true},
		},
	}}
}

func preLead(w time.Duration) time.Duration {
	return clampDur(w/5, 80*time.Millisecond, 300*time.Millisecond)
}

func postLag(w time.Duration) time.Duration {
	return clampDur(w/20, 40*time.Millisecond, 120*time.Millisecond)
}

func clampDur(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func boundaryVerdict(p spec.Policy, tl observe.Timeline) Verdict {
	n := p.Rate.Count
	metrics := map[string]float64{"limit": float64(n)}
	if held(tl, "pre") || held(tl, "post") {
		return v("boundary", Inconclusive, "the window edge was already past, so the burst was not sent", metrics)
	}
	pre := clean(marked(tl, "pre"))
	post := clean(marked(tl, "post"))
	if len(pre) == 0 || len(post) == 0 {
		return v("boundary", Inconclusive, "the window-edge bursts were not both sent", metrics)
	}
	edge := pre[0].Edge
	if edge.IsZero() {
		edge = post[0].Edge
	}
	preAllowed := countAllowed(pre)
	postAllowed := countAllowed(post)
	metrics["pre_allowed"] = float64(preAllowed)
	metrics["post_allowed"] = float64(postAllowed)
	if edge.IsZero() || !allBefore(pre, edge) || !allAfter(post, edge) {
		return v("boundary", Inconclusive, "the two bursts did not land on opposite sides of the window edge", metrics)
	}
	gap := earliest(post).Sub(earliest(pre))
	if gap < 0 {
		gap = 0
	}
	refill := 0
	if p.Rate.Window > 0 {
		refill = int(math.Ceil(float64(n) * float64(gap) / float64(p.Rate.Window)))
	}
	tol := tolerance(n)
	if preAllowed >= n-tol && postAllowed <= refill+1 {
		return v("boundary", Pass, fmt.Sprintf("second burst allowed %d after the edge, limit is %d", postAllowed, n), metrics)
	}
	if preAllowed >= n-tol && postAllowed >= n-tol {
		peak := preAllowed + postAllowed
		return v("boundary", Fail, fmt.Sprintf("allowed %d requests across the window edge, limit is %d (%.1fx)", peak, n, float64(peak)/float64(n)), metrics)
	}
	return v("boundary", Warn, fmt.Sprintf("edge allowed %d then %d, limit is %d", preAllowed, postAllowed, n), metrics)
}

func clean(events []observe.Event) []observe.Event {
	var out []observe.Event
	for _, e := range events {
		if e.Err == "" && e.Origin == 0 && e.Identity == 0 {
			out = append(out, e)
		}
	}
	return out
}

func allBefore(events []observe.Event, at time.Time) bool {
	for _, e := range events {
		if e.SentAt.IsZero() || !e.SentAt.Before(at) {
			return false
		}
	}
	return true
}

func allAfter(events []observe.Event, at time.Time) bool {
	for _, e := range events {
		if e.SentAt.IsZero() || !e.SentAt.After(at) {
			return false
		}
	}
	return true
}

func earliest(events []observe.Event) time.Time {
	var t time.Time
	for _, e := range events {
		if t.IsZero() || e.SentAt.Before(t) {
			t = e.SentAt
		}
	}
	return t
}
