package check

import (
	"fmt"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// AccuracyCheck sends a steady run and counts how many are allowed.
func AccuracyCheck() Check {
	return Check{
		Name:             "accuracy",
		IncludeByDefault: always,
		Stimulus: func(in spec.Input) sched.Stimulus {
			n := in.Policy.Rate.Count
			return sched.Stimulus{Waves: []sched.Wave{{
				Name: "steady", Marker: "steady",
				Count: n + extra(n), InFlight: 1,
				Interval: steadyGap(n, in.Policy.Rate.Window),
				Gate:     spec.Gate{Kind: spec.Ready},
			}}}
		},
		Verdict: accuracyVerdict,
	}
}

func accuracyVerdict(p spec.Policy, tl observe.Timeline) Verdict {
	n := p.Rate.Count
	ev := marked(tl, "steady")
	sent, allowed := 0, 0
	for _, e := range ev {
		if e.Origin != 0 || e.Identity != 0 || e.Err != "" {
			continue
		}
		sent++
		if e.OK() {
			allowed++
		}
	}
	metrics := map[string]float64{"sent": float64(sent), "allowed": float64(allowed), "limit": float64(n)}
	if sent < n {
		return v("accuracy", Warn, fmt.Sprintf("steady run sent %d requests, which is under the limit of %d", sent, n), metrics)
	}
	if abs(allowed-n) <= tolerance(n) {
		return v("accuracy", Pass, fmt.Sprintf("steady run allowed %d requests, limit is %d", allowed, n), metrics)
	}
	return v("accuracy", Fail, fmt.Sprintf("steady run allowed %d requests, limit is %d", allowed, n), metrics)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
