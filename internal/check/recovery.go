package check

import (
	"fmt"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// RecoveryCheck fills the limit, then arms one follower from the 429.
func RecoveryCheck() Check {
	return Check{
		Name:             "recovery",
		IncludeByDefault: always,
		Stimulus: func(in spec.Input) sched.Stimulus {
			n := in.Policy.Rate.Count
			return sched.Stimulus{Waves: []sched.Wave{{
				Name: "exhaust", Marker: "exhaust",
				Count: n + 2, InFlight: n + 2,
				Gate: spec.Gate{Kind: spec.Ready},
			}}}
		},
		Verdict: recoveryVerdict,
		Follow:  recoveryFollow,
	}
}

// recoveryFollow reads the sealed exhaust log and returns one wave.
// That wave carries its own fire times, so it cannot be deferred again.
func recoveryFollow(tl observe.Timeline, now time.Time) (sched.Wave, bool) {
	for _, e := range tl.Events {
		if e.Marker != "exhaust" || e.Status != 429 || e.Err != "" {
			continue
		}
		reset, _, ok := e.Reading.Deadline(e.SentAt)
		if !ok {
			continue
		}
		var at []time.Time
		early := now.Add(80 * time.Millisecond)
		if early.Before(reset.Add(-50 * time.Millisecond)) {
			at = append(at, early)
		}
		at = append(at, reset.Add(80*time.Millisecond))
		return sched.Wave{
			Name: "follower", Marker: "follower",
			At: at, InFlight: 1, Identity: e.Identity, Follower: true,
			Gate: spec.Gate{Kind: spec.RetryAfter},
		}, true
	}
	return sched.Wave{}, false
}

func recoveryVerdict(_ spec.Policy, tl observe.Timeline) Verdict {
	var reset time.Time
	var heard time.Time
	source := ""
	for _, e := range marked(tl, "exhaust") {
		if e.Status != 429 || e.Err != "" {
			continue
		}
		d, src, ok := e.Reading.Deadline(e.SentAt)
		if !ok {
			continue
		}
		reset = d
		source = src
		heard = e.SentAt
		break
	}
	if reset.IsZero() {
		return v("recovery", Inconclusive, "no 429 included a reset time, so recovery was not checked", nil)
	}
	slack := 40 * time.Millisecond
	if source == "retry-after" {
		slack = time.Second
	}
	recovered := false
	for _, e := range marked(tl, "follower") {
		if e.Err != "" || !e.OK() {
			continue
		}
		// Ignore anything that was in flight before the 429 came back.
		if !heard.IsZero() && !e.SentAt.After(heard) {
			continue
		}
		if e.SentAt.Before(reset.Add(-slack)) {
			return v("recovery", Fail, fmt.Sprintf("a request succeeded %s before the advertised reset", reset.Sub(e.SentAt).Round(time.Millisecond)), nil)
		}
		recovered = true
	}
	if !recovered {
		return v("recovery", Inconclusive, "no request landed after the advertised reset", nil)
	}
	return v("recovery", Pass, "the next success was at or after the advertised reset", nil)
}
