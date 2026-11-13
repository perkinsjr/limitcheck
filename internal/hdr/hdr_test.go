package hdr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteAndInterpretRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reset := now.Add(2 * time.Second)
	rec := httptest.NewRecorder()
	Write(rec, ShapeFull, Decision{
		Allowed: false, Limit: 5, Remaining: 0, Reset: reset, Window: time.Second, Now: now,
	})
	res := rec.Result()
	body := rec.Body.String()
	got := Interpret(res.StatusCode, res.Header, body, now)
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !got.HasRetry || !got.HasStandard || !got.HasLegacy || !got.HasBodyReset {
		t.Fatalf("%+v", got)
	}
	if !got.BodyReset.Equal(reset.Truncate(time.Millisecond)) {
		t.Fatalf("body reset %s want %s", got.BodyReset, reset)
	}
	if got.Remaining != 0 || got.Limit != 5 {
		t.Fatalf("limit %d remaining %d", got.Limit, got.Remaining)
	}
}

func TestWrongShapeIsNotA429(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, ShapeWrong, Decision{})
	res := rec.Result()
	got := Interpret(res.StatusCode, res.Header, rec.Body.String(), time.Now())
	if got.Status != http.StatusServiceUnavailable || got.HasRetry || got.HasStandard {
		t.Fatalf("%+v body %q", got, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "slow down") {
		t.Fatal(rec.Body.String())
	}
}

func TestNestedBodyReset(t *testing.T) {
	h := make(http.Header)
	got := Interpret(429, h, `{"error":{"reset":1700000000000}}`, time.Unix(1_700_000_000, 0))
	if !got.HasBodyReset || got.BodyReset.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("%+v", got)
	}
}
