package check

import (
	"fmt"
	"strings"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// IsolationCheck is on when two identities or --key-by are set.
func IsolationCheck() Check {
	return Check{
		Name: "isolation",
		IncludeByDefault: func(in spec.Input) bool {
			return len(in.Identities) >= 2 || len(in.KeyBy) > 0
		},
		Stimulus: isolationStimulus,
		Verdict:  isolationVerdict,
	}
}

func isolationStimulus(in spec.Input) sched.Stimulus {
	n := in.Policy.Rate.Count
	var waves []sched.Wave
	if len(in.Identities) >= 2 {
		waves = append(waves, sched.Wave{
			Name: "exhaust", Marker: "exhaust",
			Count: n + 2, InFlight: n + 2, Identity: 0,
			Gate: spec.Gate{Kind: spec.Ready},
		})
		waves = append(waves, sched.Wave{
			Name: "other", Marker: "other",
			Count: 1, InFlight: 1, Identity: 1,
			Gate:   spec.Gate{Kind: spec.AfterPrevious},
			Offset: 30 * time.Millisecond,
		})
	}
	waves = append(waves, sched.Wave{
		Name: "rotate", Marker: "rotate",
		Count: n + extra(n), InFlight: 1,
		Interval: steadyGap(n, in.Policy.Rate.Window),
		Gate:     spec.Gate{Kind: spec.Ready},
	})
	return sched.Stimulus{Waves: waves}
}

func isolationVerdict(p spec.Policy, tl observe.Timeline) Verdict {
	n := p.Rate.Count
	var problems []string
	metrics := map[string]float64{"limit": float64(n)}
	others := marked(tl, "other")
	if len(others) > 0 {
		e := others[0]
		switch {
		case e.Err != "":
			problems = append(problems, "the second identity request failed before the server answered")
		case !e.OK():
			problems = append(problems, "the second identity was rejected after the first identity filled the limit")
		}
	}
	sent, allowed := 0, 0
	for _, e := range marked(tl, "rotate") {
		if e.Identity != 0 || e.Origin != 0 || e.Err != "" {
			continue
		}
		sent++
		if e.OK() {
			allowed++
		}
	}
	metrics["allowed"] = float64(allowed)
	if sent > n && allowed > n+tolerance(n) {
		problems = append(problems, fmt.Sprintf("changing X-Limitcheck-Request on each request allowed %d, limit is %d", allowed, n))
	}
	if len(problems) > 0 {
		return v("isolation", Fail, strings.Join(problems, ". "), metrics)
	}
	if len(others) == 0 {
		return v("isolation", Warn, "only one identity was set, so this checked that a fresh X-Limitcheck-Request does not raise the limit", metrics)
	}
	return v("isolation", Pass, "the second identity was still allowed, and a fresh request id did not raise the limit", metrics)
}
