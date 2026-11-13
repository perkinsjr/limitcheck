package check

import (
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/hdr"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestBoundaryVerdicts(t *testing.T) {
	policy := spec.Policy{Rate: spec.Rate{Count: 5, Window: time.Second}}
	edge := time.Unix(1_700_000_000, 0)
	pass := observe.Timeline{Events: append(side("pre", edge.Add(-100*time.Millisecond), edge, 5, 0), side("post", edge.Add(40*time.Millisecond), edge, 0, 5)...)}
	if got := boundaryVerdict(policy, pass); got.Status != Pass {
		t.Fatal(got)
	}
	leak := observe.Timeline{Events: append(side("pre", edge.Add(-100*time.Millisecond), edge, 5, 0), side("post", edge.Add(40*time.Millisecond), edge, 5, 0)...)}
	got := boundaryVerdict(policy, leak)
	if got.Status != Fail {
		t.Fatal(got)
	}
	held := observe.Timeline{Held: []string{"pre"}}
	if got := boundaryVerdict(policy, held); got.Status != Inconclusive {
		t.Fatal(got)
	}
}

func TestBurstAndAccuracyVerdicts(t *testing.T) {
	policy := spec.Policy{Rate: spec.Rate{Count: 5, Window: time.Second}}
	burst := observe.Timeline{Events: side("burst", time.Now(), time.Time{}, 8, 0)}
	if got := burstVerdict(policy, burst); got.Status != Fail {
		t.Fatal(got)
	}
	ok := observe.Timeline{Events: side("burst", time.Now(), time.Time{}, 5, 2)}
	if got := burstVerdict(policy, ok); got.Status != Pass {
		t.Fatal(got)
	}
	steady := observe.Timeline{Events: side("steady", time.Now(), time.Time{}, 5, 2)}
	if got := accuracyVerdict(policy, steady); got.Status != Pass {
		t.Fatal(got)
	}
	open := observe.Timeline{Events: side("steady", time.Now(), time.Time{}, 7, 0)}
	if got := accuracyVerdict(policy, open); got.Status != Fail {
		t.Fatal(got)
	}
}

func TestHeadersVerdict(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	good := hdr.Reading{
		Status: 429, HasRetry: true, RetryAt: now.Add(time.Second),
		HasStandard: true, HasLegacy: true, HasBodyReset: true, BodyReset: now.Add(time.Second),
		HasRemaining: true, Remaining: 0, HasLimit: true, Limit: 5,
	}
	allow := func(rem int, at time.Time) observe.Event {
		return observe.Event{
			Marker: "sample", SentAt: at, Status: 200,
			Reading: hdr.Reading{Status: 200, Allowed: true, HasRemaining: true, Remaining: rem},
		}
	}
	tl := observe.Timeline{Events: []observe.Event{
		allow(4, now),
		allow(3, now.Add(time.Millisecond)),
		{Marker: "sample", SentAt: now.Add(2 * time.Millisecond), Status: 429, Reading: good},
	}}
	if got := headersVerdict(spec.Policy{}, tl); got.Status != Pass {
		t.Fatal(got)
	}
	bad := tl
	bad.Events = append([]observe.Event{}, tl.Events...)
	bad.Events[2].Status = 503
	bad.Events[2].Reading = hdr.Reading{Status: 503}
	if got := headersVerdict(spec.Policy{}, bad); got.Status != Fail {
		t.Fatal(got)
	}
	up := observe.Timeline{Events: []observe.Event{allow(1, now), allow(4, now.Add(time.Millisecond)), tl.Events[2]}}
	if got := headersVerdict(spec.Policy{}, up); got.Status != Fail {
		t.Fatal(got)
	}
}

func TestRecoveryIgnoresInFlightSibling(t *testing.T) {
	reset := time.Unix(1_700_000_100, 0)
	denied := observe.Event{
		Marker: "exhaust", Status: 429, SentAt: reset.Add(-900 * time.Millisecond),
		Reading: hdr.Reading{Status: 429, HasBodyReset: true, BodyReset: reset, HasRetry: true},
	}
	sibling := observe.Event{
		Marker: "exhaust", Status: 200, SentAt: reset.Add(-990 * time.Millisecond),
		Reading: hdr.Reading{Status: 200, Allowed: true},
	}
	early := observe.Event{
		Marker: "follower", Status: 200, SentAt: reset.Add(-800 * time.Millisecond),
		Reading: hdr.Reading{Status: 200, Allowed: true},
	}
	late := observe.Event{
		Marker: "follower", Status: 200, SentAt: reset.Add(50 * time.Millisecond),
		Reading: hdr.Reading{Status: 200, Allowed: true},
	}
	if got := recoveryVerdict(spec.Policy{}, observe.Timeline{Events: []observe.Event{sibling, denied, late}}); got.Status != Pass {
		t.Fatal(got)
	}
	if got := recoveryVerdict(spec.Policy{}, observe.Timeline{Events: []observe.Event{denied, early, late}}); got.Status != Fail {
		t.Fatal(got)
	}
}

func TestIsolationAndMulti(t *testing.T) {
	policy := spec.Policy{Rate: spec.Rate{Count: 5, Window: time.Second}}
	now := time.Now()
	otherDenied := observe.Timeline{Events: []observe.Event{{
		Marker: "other", Identity: 1, Status: 429, SentAt: now,
	}}}
	if got := isolationVerdict(policy, otherDenied); got.Status != Fail {
		t.Fatal(got)
	}
	rotated := side("rotate", now, time.Time{}, 8, 0)
	if got := isolationVerdict(policy, observe.Timeline{Events: rotated}); got.Status != Fail {
		t.Fatal(got)
	}
	shared := append(sideAt("fan", 0, now, 5, 2), sideAt("fan", 1, now, 0, 7)...)
	if got := multiVerdict(policy, observe.Timeline{Events: shared}); got.Status != Pass {
		t.Fatal(got)
	}
	own := append(sideAt("fan", 0, now, 5, 2), sideAt("fan", 1, now, 5, 2)...)
	got := multiVerdict(policy, observe.Timeline{Events: own})
	if got.Status != Fail {
		t.Fatal(got)
	}
}

func side(marker string, at, edge time.Time, allow, deny int) []observe.Event {
	return build(marker, 0, at, edge, allow, deny)
}

func sideAt(marker string, origin int, at time.Time, allow, deny int) []observe.Event {
	return build(marker, origin, at, time.Time{}, allow, deny)
}

func build(marker string, origin int, at, edge time.Time, allow, deny int) []observe.Event {
	var out []observe.Event
	for i := 0; i < allow+deny; i++ {
		status := 429
		reading := hdr.Reading{Status: 429}
		if i < allow {
			status = 200
			reading = hdr.Reading{Status: 200, Allowed: true}
		}
		out = append(out, observe.Event{
			Marker: marker, Origin: origin, Identity: 0,
			SentAt: at.Add(time.Duration(i)), Edge: edge,
			Status: status, Reading: reading,
		})
	}
	return out
}
