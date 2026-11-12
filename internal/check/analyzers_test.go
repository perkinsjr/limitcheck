package check

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestAccuracy(t *testing.T) {
	c := camp(5, time.Second)
	if v := accuracy(c, steadyLog(5, 5)); v.Status != Pass {
		t.Fatalf("%s %s", v.Status, v.Message)
	}
	if v := accuracy(c, steadyLog(7, 7)); v.Status != Fail || v.Metrics["allowed"] != 7 {
		t.Fatalf("%s %s", v.Status, v.Message)
	}
	if v := accuracy(c, steadyLog(7, 2)); v.Status != Fail {
		t.Fatalf("%s %s", v.Status, v.Message)
	}
}

func TestBoundary(t *testing.T) {
	c := camp(5, time.Second)
	edge := time.Unix(1_700_000_010, 0)
	passLog := observe.Log{BoundaryAt: edge, Limit: c.Limit}
	for i := 0; i < 5; i++ {
		passLog.Events = append(passLog.Events, ev(spec.TagPreBoundary, 0, 200, edge.Add(-100*time.Millisecond), nil, ""))
	}
	for i := 0; i < 5; i++ {
		passLog.Events = append(passLog.Events, ev(spec.TagPostBoundary, 0, 429, edge.Add(40*time.Millisecond), nil, ""))
	}
	if v := boundary(c, passLog); v.Status != Pass {
		t.Fatalf("pass case: %s %s", v.Status, v.Message)
	}

	failLog := observe.Log{BoundaryAt: edge, Limit: c.Limit}
	for i := 0; i < 5; i++ {
		failLog.Events = append(failLog.Events, ev(spec.TagPreBoundary, 0, 200, edge.Add(-100*time.Millisecond), nil, ""))
		failLog.Events = append(failLog.Events, ev(spec.TagPostBoundary, 0, 200, edge.Add(40*time.Millisecond), nil, ""))
	}
	v := boundary(c, failLog)
	if v.Status != Fail {
		t.Fatalf("leak case: %s %s", v.Status, v.Message)
	}
	if v.Metrics["pre_allowed"] != 5 || v.Metrics["post_allowed"] != 5 {
		t.Fatalf("metrics %v", v.Metrics)
	}
}

func TestBurst(t *testing.T) {
	c := camp(5, time.Second)
	log := observe.Log{}
	for i := 0; i < 5; i++ {
		log.Events = append(log.Events, ev(spec.TagBurst, 0, 200, time.Unix(10, 0), nil, ""))
	}
	if v := burst(c, log); v.Status != Pass {
		t.Fatalf("%s %s", v.Status, v.Message)
	}
	log.Events = append(log.Events, ev(spec.TagBurst, 0, 200, time.Unix(10, 0), nil, ""))
	log.Events = append(log.Events, ev(spec.TagBurst, 0, 200, time.Unix(10, 0), nil, ""))
	v := burst(c, log)
	if v.Status != Fail || v.Metrics["allowed"] != 7 {
		t.Fatalf("%s %v", v.Message, v.Metrics)
	}
}

func TestIsolation(t *testing.T) {
	c := camp(5, time.Second)
	c.Identities = []spec.Identity{{Name: "a"}, {Name: "b"}}
	shared := observe.Log{Events: []observe.Event{
		ev(spec.TagProbe, 1, 429, time.Unix(10, 0), nil, ""),
	}}
	shared.Events = append(shared.Events, steadyEvents(7, 5)...)
	if v := isolation(c, shared); v.Status != Fail {
		t.Fatalf("shared bucket: %s %s", v.Status, v.Message)
	}

	rotate := observe.Log{Events: []observe.Event{
		ev(spec.TagProbe, 1, 200, time.Unix(10, 0), nil, ""),
	}}
	rotate.Events = append(rotate.Events, steadyEvents(7, 7)...)
	v := isolation(c, rotate)
	if v.Status != Fail {
		t.Fatalf("rotate: %s %s", v.Status, v.Message)
	}
	if !contains(v.Message, "X-Limitcheck-Request") {
		t.Fatalf("message %q", v.Message)
	}

	ok := observe.Log{Events: []observe.Event{
		ev(spec.TagProbe, 1, 200, time.Unix(10, 0), nil, ""),
	}}
	ok.Events = append(ok.Events, steadyEvents(7, 5)...)
	if v := isolation(c, ok); v.Status != Pass {
		t.Fatalf("isolated: %s %s", v.Status, v.Message)
	}
}

func TestHeaders(t *testing.T) {
	c := camp(5, time.Second)
	good := observe.Log{Events: []observe.Event{
		ev(spec.TagSteady, 0, 200, time.Unix(10, 0), map[string]string{"X-RateLimit-Remaining": "2"}, ""),
		ev(spec.TagSteady, 0, 200, time.Unix(10, int64(time.Millisecond)), map[string]string{"X-RateLimit-Remaining": "1"}, ""),
		rejected(time.Unix(11, 0), 429, `{"reset":1700000011}`, map[string]string{
			"Retry-After":           "1",
			"RateLimit":             `"default";r=0;t=1`,
			"X-RateLimit-Limit":     "5",
			"X-RateLimit-Remaining": "0",
			"RateLimit-Policy":      `"default";q=5;w=1`,
		}),
	}}
	if v := headersCheck(c, good); v.Status != Pass {
		t.Fatalf("good: %s %s", v.Status, v.Message)
	}

	wrong := good
	wrong.Events = append([]observe.Event{}, good.Events...)
	wrong.Events[2].Status = 503
	if v := headersCheck(c, wrong); v.Status != Fail || !contains(v.Message, "503") {
		t.Fatalf("status: %s %s", v.Status, v.Message)
	}

	noRetry := good
	noRetry.Events = append([]observe.Event{}, good.Events...)
	noRetry.Events[2].Header = noRetry.Events[2].Header.Clone()
	noRetry.Events[2].Header.Del("Retry-After")
	if v := headersCheck(c, noRetry); v.Status != Fail || !contains(v.Message, "Retry-After") {
		t.Fatalf("retry: %s %s", v.Status, v.Message)
	}

	badBody := good
	badBody.Events = append([]observe.Event{}, good.Events...)
	badBody.Events[2].Body = "slow down"
	if v := headersCheck(c, badBody); v.Status != Fail || !contains(v.Message, "JSON") {
		t.Fatalf("body: %s %s", v.Status, v.Message)
	}

	up := observe.Log{Events: []observe.Event{
		ev(spec.TagSteady, 0, 200, time.Unix(10, 0), map[string]string{"X-RateLimit-Remaining": "1"}, ""),
		ev(spec.TagSteady, 0, 200, time.Unix(10, int64(time.Millisecond)), map[string]string{"X-RateLimit-Remaining": "4"}, ""),
		rejected(time.Unix(11, 0), 429, `{"reset":1700000011}`, map[string]string{"Retry-After": "1", "X-RateLimit-Remaining": "0"}),
	}}
	if v := headersCheck(c, up); v.Status != Fail || !contains(v.Message, "remaining") {
		t.Fatalf("remaining: %s %s", v.Status, v.Message)
	}
}

func TestRecovery(t *testing.T) {
	c := camp(5, time.Second)
	reset := time.Unix(1_700_000_010, 0)
	body := `{"reset":1700000010}`
	early := observe.Log{Events: []observe.Event{
		rejected(reset.Add(-time.Second), 429, body, map[string]string{"Retry-After": "1"}),
		ev(spec.TagProbe, 0, 200, reset.Add(-500*time.Millisecond), nil, ""),
	}}
	if v := recovery(c, early); v.Status != Fail {
		t.Fatalf("early: %s %s", v.Status, v.Message)
	}
	onTime := observe.Log{Events: []observe.Event{
		rejected(reset.Add(-time.Second), 429, body, map[string]string{"Retry-After": "1"}),
		ev(spec.TagProbe, 0, 429, reset.Add(-400*time.Millisecond), nil, body),
		ev(spec.TagProbe, 0, 200, reset.Add(100*time.Millisecond), nil, ""),
	}}
	if v := recovery(c, onTime); v.Status != Pass {
		t.Fatalf("on time: %s %s", v.Status, v.Message)
	}
	if v := recovery(c, observe.Log{}); v.Status != Warn {
		t.Fatalf("empty: %s", v.Status)
	}
}

func TestMultiInstance(t *testing.T) {
	c := camp(5, time.Second)
	c.Origins = []spec.Origin{{Scheme: "http", Host: "127.0.0.1:1"}, {Scheme: "http", Host: "127.0.0.1:2"}}
	separate := observe.Log{}
	for o := 0; o < 2; o++ {
		for i := 0; i < 5; i++ {
			separate.Events = append(separate.Events, ev(spec.TagBurst, 0, 200, time.Unix(10, 0), nil, ""))
			separate.Events[len(separate.Events)-1].Origin = o
		}
	}
	if v := multiInstance(c, separate); v.Status != Fail {
		t.Fatalf("separate: %s %s", v.Status, v.Message)
	}
	shared := observe.Log{}
	for i := 0; i < 5; i++ {
		e := ev(spec.TagBurst, 0, 200, time.Unix(10, 0), nil, "")
		e.Origin = i % 2
		shared.Events = append(shared.Events, e)
	}
	if v := multiInstance(c, shared); v.Status != Pass {
		t.Fatalf("shared: %s %s", v.Status, v.Message)
	}
	if v := multiInstance(camp(5, time.Second), shared); v.Status != Warn {
		t.Fatalf("one origin: %s", v.Status)
	}
}

func TestBodyResetNested(t *testing.T) {
	got, ok := bodyReset(`{"error":{"reset":1700000000000}}`, time.Unix(0, 0))
	if !ok || got.UnixMilli() != 1700000000000 {
		t.Fatalf("got %s ok %v", got, ok)
	}
	if _, ok := bodyReset(`slow down`, time.Now()); ok {
		t.Fatal("plain text parsed")
	}
}

func camp(n int, window time.Duration) spec.Campaign {
	return spec.Campaign{
		Limit:      spec.Limit{Count: n, Window: window},
		Identities: []spec.Identity{{Name: "default"}},
		Origins:    []spec.Origin{{Scheme: "http", Host: "127.0.0.1"}},
		Checks:     append([]spec.CheckKind{}, spec.DefaultChecks...),
	}
}

func ev(tag spec.Tag, identity, status int, started time.Time, header map[string]string, body string) observe.Event {
	h := http.Header{}
	for k, v := range header {
		h.Set(k, v)
	}
	return observe.Event{
		Tag:      tag,
		Identity: identity,
		Status:   status,
		Started:  started,
		Ended:    started.Add(time.Millisecond),
		Header:   h,
		Body:     body,
	}
}

func rejected(started time.Time, status int, body string, header map[string]string) observe.Event {
	e := ev(spec.TagProbe, 0, status, started, header, body)
	return e
}

func steadyLog(sent, allowed int) observe.Log {
	return observe.Log{Events: steadyEvents(sent, allowed)}
}

func steadyEvents(sent, allowed int) []observe.Event {
	out := make([]observe.Event, sent)
	base := time.Unix(20, 0)
	for i := 0; i < sent; i++ {
		status := 429
		if i < allowed {
			status = 200
		}
		out[i] = ev(spec.TagSteady, 0, status, base.Add(time.Duration(i)*time.Millisecond), nil, "")
	}
	return out
}

func contains(s, part string) bool {
	return strings.Contains(s, part)
}
