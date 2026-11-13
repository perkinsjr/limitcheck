package check

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func accuracy(c spec.Campaign, log observe.Log) Verdict {
	n := c.Limit.Count
	sent, allowed := 0, 0
	for _, e := range log.Events {
		if e.Tag != spec.TagSteady || e.Origin != 0 || e.Identity != 0 || e.Err != "" {
			continue
		}
		sent++
		if e.Allowed() {
			allowed++
		}
	}
	metrics := map[string]float64{"sent": float64(sent), "allowed": float64(allowed), "limit": float64(n)}
	if sent < n {
		return verdict(spec.CheckAccuracy, Warn, fmt.Sprintf("steady run sent %d requests, which is under the limit of %d", sent, n), metrics)
	}
	if abs(allowed-n) <= tolerance(n) {
		return verdict(spec.CheckAccuracy, Pass, fmt.Sprintf("steady run allowed %d requests, limit is %d", allowed, n), metrics)
	}
	return verdict(spec.CheckAccuracy, Fail, fmt.Sprintf("steady run allowed %d requests, limit is %d", allowed, n), metrics)
}

func boundary(c spec.Campaign, log observe.Log) Verdict {
	n := c.Limit.Count
	var pre, post []observe.Event
	for _, e := range log.Events {
		if e.Err != "" || e.Origin != 0 || e.Identity != 0 {
			continue
		}
		switch e.Tag {
		case spec.TagPreBoundary:
			pre = append(pre, e)
		case spec.TagPostBoundary:
			post = append(post, e)
		}
	}
	metrics := map[string]float64{"limit": float64(n)}
	if len(pre) == 0 || len(post) == 0 || log.BoundaryAt.IsZero() {
		return verdict(spec.CheckBoundary, Warn, "the window-edge bursts were not both sent", metrics)
	}
	preAllowed := countAllowed(pre)
	postAllowed := countAllowed(post)
	metrics["pre_allowed"] = float64(preAllowed)
	metrics["post_allowed"] = float64(postAllowed)
	if !allBefore(pre, log.BoundaryAt) || !allAfter(post, log.BoundaryAt) {
		return verdict(spec.CheckBoundary, Warn, "the two bursts did not land on opposite sides of the window edge", metrics)
	}
	gap := earliest(post).Sub(earliest(pre))
	if gap < 0 {
		gap = 0
	}
	refill := 0
	if c.Limit.Window > 0 {
		refill = int(math.Ceil(float64(n) * float64(gap) / float64(c.Limit.Window)))
	}
	tol := tolerance(n)
	if preAllowed >= n-tol && postAllowed <= refill+1 {
		return verdict(spec.CheckBoundary, Pass, fmt.Sprintf("second burst allowed %d after the edge, limit is %d", postAllowed, n), metrics)
	}
	if preAllowed >= n-tol && postAllowed >= n-tol {
		peak := preAllowed + postAllowed
		return verdict(spec.CheckBoundary, Fail, fmt.Sprintf("allowed %d requests across the window edge, limit is %d (%.1fx)", peak, n, float64(peak)/float64(n)), metrics)
	}
	return verdict(spec.CheckBoundary, Warn, fmt.Sprintf("edge allowed %d then %d, limit is %d", preAllowed, postAllowed, n), metrics)
}

func burst(c spec.Campaign, log observe.Log) Verdict {
	n := c.Limit.Count
	per := map[int]int{}
	sent := 0
	for _, e := range log.Events {
		if e.Tag != spec.TagBurst || e.Err != "" {
			continue
		}
		sent++
		if e.Allowed() {
			per[e.Origin]++
		}
	}
	metrics := map[string]float64{"sent": float64(sent), "limit": float64(n)}
	if sent == 0 {
		return verdict(spec.CheckBurst, Warn, "no burst was sent", metrics)
	}
	worst := 0
	for _, allowed := range per {
		if allowed > worst {
			worst = allowed
		}
	}
	metrics["allowed"] = float64(worst)
	if worst > n {
		return verdict(spec.CheckBurst, Fail, fmt.Sprintf("concurrent burst allowed %d requests, limit is %d", worst, n), metrics)
	}
	return verdict(spec.CheckBurst, Pass, fmt.Sprintf("concurrent burst allowed %d requests, limit is %d", worst, n), metrics)
}

func isolation(c spec.Campaign, log observe.Log) Verdict {
	n := c.Limit.Count
	var problems []string
	metrics := map[string]float64{"limit": float64(n)}
	if len(c.Identities) > 1 {
		var second *observe.Event
		for i := range log.Events {
			e := &log.Events[i]
			if e.Tag == spec.TagProbe && e.Identity == 1 {
				second = e
				break
			}
		}
		switch {
		case second == nil:
			problems = append(problems, "no request was sent as the second identity")
		case second.Err != "":
			problems = append(problems, "the second identity request failed before the server answered")
		case !second.Allowed():
			problems = append(problems, "the second identity was rejected after the first identity filled the limit")
		}
	}
	sent, allowed := 0, 0
	for _, e := range log.Events {
		if e.Tag != spec.TagSteady || e.Identity != 0 || e.Origin != 0 || e.Err != "" {
			continue
		}
		sent++
		if e.Allowed() {
			allowed++
		}
	}
	metrics["allowed"] = float64(allowed)
	if sent > n && allowed > n+tolerance(n) {
		problems = append(problems, fmt.Sprintf("changing X-Limitcheck-Request on each request allowed %d, limit is %d", allowed, n))
	}
	if len(problems) > 0 {
		return verdict(spec.CheckIsolation, Fail, strings.Join(problems, ". "), metrics)
	}
	if len(c.Identities) < 2 {
		return verdict(spec.CheckIsolation, Warn, "only one identity was set, so this checked that a fresh X-Limitcheck-Request does not raise the limit", metrics)
	}
	return verdict(spec.CheckIsolation, Pass, "the second identity was still allowed, and a fresh request id did not raise the limit", metrics)
}

func headersCheck(c spec.Campaign, log observe.Log) Verdict {
	var problems []string
	saw429 := false
	for _, e := range log.Events {
		if e.Err != "" {
			continue
		}
		if e.Status >= 400 && e.Status != httpTooMany {
			problems = append(problems, fmt.Sprintf("rejected with status %d, want 429", e.Status))
			break
		}
		if e.Status != httpTooMany {
			continue
		}
		saw429 = true
		if e.Header.Get("Retry-After") == "" {
			problems = append(problems, "429 is missing Retry-After")
		}
		if !hasRateLimit(e.Header) && !hasXRateLimit(e.Header) {
			problems = append(problems, "429 has neither RateLimit nor X-RateLimit headers")
		}
		if _, ok := bodyReset(e.Body, e.Ended); !ok {
			problems = append(problems, "429 body is not JSON with a reset time")
		}
		if msg, bad := limitsDisagree(e.Header); bad {
			problems = append(problems, msg)
		}
		if len(problems) > 0 {
			break
		}
	}
	if msg, bad := remainingIncreases(log); bad {
		problems = append(problems, msg)
	}
	if len(problems) > 0 {
		return verdict(spec.CheckHeaders, Fail, problems[0], nil)
	}
	if !saw429 {
		return verdict(spec.CheckHeaders, Warn, "no 429 response, so Retry-After and the response body were not checked", nil)
	}
	return verdict(spec.CheckHeaders, Pass, "429 responses include Retry-After, a rate-limit header, and a JSON reset, and remaining does not go up", nil)
}

const httpTooMany = 429

func remainingIncreases(log observe.Log) (string, bool) {
	var seq []observe.Event
	for _, e := range log.Events {
		if e.Tag == spec.TagSteady && e.Identity == 0 && e.Origin == 0 && e.Allowed() {
			seq = append(seq, e)
		}
	}
	sort.Slice(seq, func(i, j int) bool { return seq[i].Started.Before(seq[j].Started) })
	prev := -1
	for _, e := range seq {
		rem, ok := remainingOf(e.Header)
		if !ok {
			continue
		}
		if prev >= 0 && rem > prev {
			return fmt.Sprintf("remaining went from %d to %d", prev, rem), true
		}
		prev = rem
	}
	return "", false
}

func recovery(c spec.Campaign, log observe.Log) Verdict {
	type key struct {
		origin   int
		identity int
	}
	groups := map[key][]observe.Event{}
	for _, e := range log.Events {
		if e.Err != "" {
			continue
		}
		k := key{e.Origin, e.Identity}
		groups[k] = append(groups[k], e)
	}
	sawDeadline := false
	sawSuccessAfter := false
	for _, evs := range groups {
		sort.Slice(evs, func(i, j int) bool { return evs[i].Started.Before(evs[j].Started) })
		var until time.Time
		var heard time.Time
		source := ""
		blocked := false
		for _, e := range evs {
			if e.Status == httpTooMany {
				d, src, ok := deadline(e)
				if !ok {
					continue
				}
				sawDeadline = true
				until = d
				heard = e.Ended
				if heard.IsZero() {
					heard = e.Started
				}
				source = src
				blocked = true
				continue
			}
			if !blocked || !e.Allowed() {
				continue
			}
			// A sibling from the same burst can start after the 429 was
			// recorded and still have been sent before that 429 came back.
			if !e.Started.After(heard) {
				continue
			}
			slack := 40 * time.Millisecond
			if source == "retry-after" {
				slack = time.Second
			}
			if e.Started.Before(until.Add(-slack)) {
				return verdict(spec.CheckRecovery, Fail, fmt.Sprintf("a request succeeded %s before the advertised reset", until.Sub(e.Started).Round(time.Millisecond)), nil)
			}
			sawSuccessAfter = true
			blocked = false
		}
	}
	if !sawDeadline {
		return verdict(spec.CheckRecovery, Warn, "no 429 included a reset time, so recovery was not checked", nil)
	}
	if !sawSuccessAfter {
		return verdict(spec.CheckRecovery, Warn, "no request landed after the advertised reset", nil)
	}
	return verdict(spec.CheckRecovery, Pass, "the next success was at or after the advertised reset", nil)
}

func multiInstance(c spec.Campaign, log observe.Log) Verdict {
	n := c.Limit.Count
	metrics := map[string]float64{"limit": float64(n), "origins": float64(len(c.Origins))}
	if len(c.Origins) < 2 {
		return verdict(spec.CheckMultiInstance, Warn, "need at least two --origin values to compare hosts", metrics)
	}
	sent := map[int]bool{}
	allowedCount := map[int]int{}
	for _, e := range log.Events {
		if e.Tag != spec.TagBurst || e.Err != "" {
			continue
		}
		sent[e.Origin] = true
		if e.Allowed() {
			allowedCount[e.Origin]++
		}
	}
	if len(sent) < 2 {
		return verdict(spec.CheckMultiInstance, Warn, "the burst did not reach two origins", metrics)
	}
	total := 0
	minAllowed := -1
	for origin := range sent {
		nAllowed := allowedCount[origin]
		total += nAllowed
		if minAllowed < 0 || nAllowed < minAllowed {
			minAllowed = nAllowed
		}
	}
	metrics["allowed"] = float64(total)
	tol := tolerance(n)
	if minAllowed >= n-tol && total >= len(sent)*(n-tol) {
		return verdict(spec.CheckMultiInstance, Fail, fmt.Sprintf("each origin allowed about %d requests on its own (%d total, limit %d)", minAllowed, total, n), metrics)
	}
	if total <= n+tol {
		return verdict(spec.CheckMultiInstance, Pass, fmt.Sprintf("the origins shared one limit and allowed %d requests, limit is %d", total, n), metrics)
	}
	return verdict(spec.CheckMultiInstance, Warn, fmt.Sprintf("origins allowed %d requests together, limit is %d", total, n), metrics)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func allBefore(events []observe.Event, at time.Time) bool {
	for _, e := range events {
		if e.Started.IsZero() || !e.Started.Before(at) {
			return false
		}
	}
	return true
}

func allAfter(events []observe.Event, at time.Time) bool {
	for _, e := range events {
		if e.Started.IsZero() || !e.Started.After(at) {
			return false
		}
	}
	return true
}

func earliest(events []observe.Event) time.Time {
	var t time.Time
	for _, e := range events {
		if t.IsZero() || e.Started.Before(t) {
			t = e.Started
		}
	}
	return t
}
