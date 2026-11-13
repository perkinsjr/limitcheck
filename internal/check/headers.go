package check

import (
	"fmt"
	"sort"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// HeadersCheck reads 429s and the remaining counter.
func HeadersCheck() Check {
	return Check{
		Name:             "headers",
		IncludeByDefault: always,
		Stimulus: func(in spec.Input) sched.Stimulus {
			n := in.Policy.Rate.Count
			return sched.Stimulus{Waves: []sched.Wave{{
				Name: "sample", Marker: "sample",
				Count: n + extra(n), InFlight: 1,
				Interval: steadyGap(n, in.Policy.Rate.Window),
				Gate:     spec.Gate{Kind: spec.Ready},
			}}}
		},
		Verdict: headersVerdict,
	}
}

func headersVerdict(_ spec.Policy, tl observe.Timeline) Verdict {
	var problems []string
	saw429 := false
	for _, e := range tl.Events {
		if e.Err != "" || e.Held {
			continue
		}
		if e.Status >= 400 && e.Status != 429 {
			problems = append(problems, fmt.Sprintf("rejected with status %d, want 429", e.Status))
			break
		}
		if e.Status != 429 {
			continue
		}
		saw429 = true
		r := e.Reading
		if !r.HasRetry {
			problems = append(problems, "429 is missing Retry-After")
		}
		if !r.HasStandard && !r.HasLegacy {
			problems = append(problems, "429 has neither RateLimit nor X-RateLimit headers")
		}
		if !r.HasBodyReset {
			problems = append(problems, "429 body is not JSON with a reset time")
		}
		if r.Disagree != "" {
			problems = append(problems, r.Disagree)
		}
		if len(problems) > 0 {
			break
		}
	}
	if msg, bad := remainingIncreases(tl); bad {
		problems = append(problems, msg)
	}
	if len(problems) > 0 {
		return v("headers", Fail, problems[0], nil)
	}
	if !saw429 {
		return v("headers", Inconclusive, "no 429 response, so Retry-After and the response body were not checked", nil)
	}
	return v("headers", Pass, "429 responses include Retry-After, a rate-limit header, and a JSON reset, and remaining does not go up", nil)
}

func remainingIncreases(tl observe.Timeline) (string, bool) {
	seq := marked(tl, "sample")
	sort.Slice(seq, func(i, j int) bool { return seq[i].SentAt.Before(seq[j].SentAt) })
	prev := -1
	for _, e := range seq {
		if !e.OK() || !e.Reading.HasRemaining {
			continue
		}
		rem := e.Reading.Remaining
		if prev >= 0 && rem > prev {
			return fmt.Sprintf("remaining went from %d to %d", prev, rem), true
		}
		prev = rem
	}
	return "", false
}
