package check

import (
	"fmt"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// MultiInstanceCheck is on when two origins and --sharing are both set.
func MultiInstanceCheck() Check {
	return Check{
		Name: "multi_instance",
		IncludeByDefault: func(in spec.Input) bool {
			return len(in.Origins) >= 2 && in.Sharing
		},
		Stimulus: func(in spec.Input) sched.Stimulus {
			return sched.Stimulus{Waves: []sched.Wave{burstWave("fan", "fan", in, true)}}
		},
		Verdict: multiVerdict,
	}
}

func multiVerdict(p spec.Policy, tl observe.Timeline) Verdict {
	n := p.Rate.Count
	sent := map[int]bool{}
	allowedCount := map[int]int{}
	for _, e := range marked(tl, "fan") {
		if e.Err != "" {
			continue
		}
		sent[e.Origin] = true
		if e.OK() {
			allowedCount[e.Origin]++
		}
	}
	metrics := map[string]float64{"limit": float64(n), "origins": float64(len(sent))}
	if len(sent) < 2 {
		return v("multi_instance", Warn, "need at least two --origin values to compare hosts", metrics)
	}
	total := 0
	minAllowed := -1
	for origin := range sent {
		got := allowedCount[origin]
		total += got
		if minAllowed < 0 || got < minAllowed {
			minAllowed = got
		}
	}
	metrics["allowed"] = float64(total)
	tol := tolerance(n)
	if minAllowed >= n-tol && total >= len(sent)*(n-tol) {
		return v("multi_instance", Fail, fmt.Sprintf("each origin allowed about %d requests on its own (%d total, limit %d)", minAllowed, total, n), metrics)
	}
	if total <= n+tol {
		return v("multi_instance", Pass, fmt.Sprintf("the origins shared one limit and allowed %d requests, limit is %d", total, n), metrics)
	}
	return v("multi_instance", Warn, fmt.Sprintf("origins allowed %d requests together, limit is %d", total, n), metrics)
}
