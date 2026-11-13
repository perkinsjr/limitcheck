package check

import (
	"fmt"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// BurstCheck fires more requests than the limit at one instant.
func BurstCheck() Check {
	return Check{
		Name:             "burst",
		IncludeByDefault: always,
		Stimulus: func(in spec.Input) sched.Stimulus {
			return sched.Stimulus{Waves: []sched.Wave{burstWave("burst", "burst", in, false)}}
		},
		Verdict: burstVerdict,
	}
}

func burstWave(name, marker string, in spec.Input, fanout bool) sched.Wave {
	n := in.Policy.Rate.Count
	return sched.Wave{
		Name: name, Marker: marker,
		Count: n + 2, InFlight: n + 2,
		Gate:   spec.Gate{Kind: spec.Ready},
		Fanout: fanout,
	}
}

func burstVerdict(p spec.Policy, tl observe.Timeline) Verdict {
	n := p.Rate.Count
	if n >= spec.DefaultMaxInFlight {
		return v("burst", Inconclusive, fmt.Sprintf("the in-flight cap is %d, which is not above the limit, so a race would not show", spec.DefaultMaxInFlight), nil)
	}
	per := map[int]int{}
	sent := 0
	for _, e := range marked(tl, "burst") {
		if e.Err != "" {
			continue
		}
		sent++
		if e.OK() {
			per[e.Origin]++
		}
	}
	metrics := map[string]float64{"sent": float64(sent), "limit": float64(n)}
	if sent == 0 {
		return v("burst", Inconclusive, "no burst was sent", metrics)
	}
	worst := 0
	for _, allowed := range per {
		if allowed > worst {
			worst = allowed
		}
	}
	metrics["allowed"] = float64(worst)
	if worst > n {
		return v("burst", Fail, fmt.Sprintf("concurrent burst allowed %d requests, limit is %d", worst, n), metrics)
	}
	return v("burst", Pass, fmt.Sprintf("concurrent burst allowed %d requests, limit is %d", worst, n), metrics)
}
