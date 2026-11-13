// Package hdr is the only code that reads or writes rate-limit bytes.
package hdr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Shape is how a response is written. The broken server uses both.
type Shape int

const (
	// ShapeFull is a normal allow or a 429 with the headers and a reset body.
	ShapeFull Shape = iota
	// ShapeWrong is a rejection that is not a 429 and carries no rate-limit headers.
	ShapeWrong
)

// Decision is the limiter's answer, before it becomes bytes.
type Decision struct {
	Allowed   bool
	Limit     int
	Remaining int
	Reset     time.Time
	Window    time.Duration
	Now       time.Time
}

// Reading is what a check is allowed to know about one response.
type Reading struct {
	Status       int
	Allowed      bool
	HasRetry     bool
	RetryAt      time.Time
	HasReset     bool
	Reset        time.Time
	HasBodyReset bool
	BodyReset    time.Time
	HasLimit     bool
	Limit        int
	HasRemaining bool
	Remaining    int
	HasStandard  bool
	HasLegacy    bool
	Disagree     string
	Body         string
}

// Deadline is the moment a denied caller was told to come back.
// The body wins, then a reset header, then Retry-After.
func (r Reading) Deadline(received time.Time) (time.Time, string, bool) {
	if r.HasBodyReset {
		return r.BodyReset, "body", true
	}
	if r.HasReset {
		return r.Reset, "header", true
	}
	if r.HasRetry {
		return r.RetryAt, "retry-after", true
	}
	return time.Time{}, "", false
}

// Write is the only writer of rate-limit responses.
func Write(w http.ResponseWriter, shape Shape, d Decision) {
	if shape == ShapeWrong {
		http.Error(w, "slow down", http.StatusServiceUnavailable)
		return
	}
	sec := retrySeconds(d.Reset, d.Now)
	w.Header().Set("RateLimit", fmt.Sprintf(`"default";r=%d;t=%d`, remaining(d), sec))
	w.Header().Set("RateLimit-Policy", fmt.Sprintf(`"default";q=%d;w=%d`, d.Limit, windowSeconds(d.Window)))
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining(d)))
	if !d.Reset.IsZero() {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(d.Reset.Unix(), 10))
	}
	if d.Allowed {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(sec))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	// Milliseconds survive a window shorter than one second. Whole seconds
	// would round a 500ms reset onto the wrong side of the edge.
	_, _ = fmt.Fprintf(w, `{"reset":%d}`, d.Reset.UnixMilli())
}

func remaining(d Decision) int {
	if d.Allowed {
		if d.Remaining < 0 {
			return 0
		}
		return d.Remaining
	}
	return 0
}

func retrySeconds(reset, now time.Time) int {
	sec := int(reset.Sub(now).Seconds())
	if sec < 1 {
		return 1
	}
	return sec
}

func windowSeconds(w time.Duration) int {
	sec := int((w + time.Second - 1) / time.Second)
	if sec < 1 {
		return 1
	}
	return sec
}

// Interpret is the only reader of rate-limit responses.
func Interpret(status int, header http.Header, body string, received time.Time) Reading {
	r := Reading{
		Status:  status,
		Allowed: status >= 200 && status < 400,
		Body:    body,
	}
	if v := header.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && sec >= 0 {
			r.HasRetry = true
			r.RetryAt = received.Add(time.Duration(sec) * time.Second)
		}
	}
	std := standard(header)
	leg := legacy(header)
	r.HasStandard = std.present
	r.HasLegacy = leg.present
	if leg.hasRem {
		r.HasRemaining = true
		r.Remaining = leg.rem
	} else if std.hasRem {
		r.HasRemaining = true
		r.Remaining = std.rem
	}
	if leg.hasLimit {
		r.HasLimit = true
		r.Limit = leg.limit
	} else if std.hasLimit {
		r.HasLimit = true
		r.Limit = std.limit
	}
	switch {
	case leg.hasReset:
		r.HasReset = true
		r.Reset = leg.reset
	case std.hasReset:
		r.HasReset = true
		r.Reset = std.reset
	}
	if std.hasRem && leg.hasRem && std.rem != leg.rem || std.hasLimit && leg.hasLimit && std.limit != leg.limit {
		r.Disagree = "RateLimit and X-RateLimit disagree"
	}
	if t, ok := bodyReset(body, received); ok {
		r.HasBodyReset = true
		r.BodyReset = t
	}
	return r
}

type wireNums struct {
	rem, limit int
	hasRem     bool
	hasLimit   bool
	reset      time.Time
	hasReset   bool
	present    bool
}

func standard(h http.Header) wireNums {
	raw := h.Get("RateLimit")
	policy := h.Get("RateLimit-Policy")
	var n wireNums
	if raw == "" && policy == "" && h.Get("RateLimit-Reset") == "" {
		return n
	}
	n.present = raw != "" || policy != ""
	if v, yes := firstInt(raw, "r"); yes {
		n.rem = v
		n.hasRem = true
	}
	if v, yes := firstInt(policy, "q"); yes {
		n.limit = v
		n.hasLimit = true
	}
	if v, yes := firstInt(h.Get("RateLimit-Reset"), ""); yes {
		n.reset = unixish(v, time.Now())
		n.hasReset = true
	}
	return n
}

func legacy(h http.Header) wireNums {
	lr := h.Get("X-RateLimit-Remaining")
	ll := h.Get("X-RateLimit-Limit")
	lz := h.Get("X-RateLimit-Reset")
	var n wireNums
	if lr == "" && ll == "" && lz == "" {
		return n
	}
	n.present = true
	if v, err := strconv.Atoi(strings.TrimSpace(lr)); err == nil {
		n.rem = v
		n.hasRem = true
	}
	if v, err := strconv.Atoi(strings.TrimSpace(ll)); err == nil {
		n.limit = v
		n.hasLimit = true
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(lz), 10, 64); err == nil {
		n.reset = unixish(int(v), time.Now())
		n.hasReset = true
	}
	return n
}

func firstInt(s, key string) (int, bool) {
	if key == "" {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		return n, err == nil
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ';' || r == ',' || r == ' '
	}) {
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.Trim(k, `"`) != key {
			continue
		}
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

func unixish(n int, now time.Time) time.Time {
	switch {
	case n > 1_000_000_000_000:
		return time.UnixMilli(int64(n))
	case n > 1_000_000_000:
		return time.Unix(int64(n), 0)
	default:
		return now.Add(time.Duration(n) * time.Second)
	}
}

func bodyReset(body string, received time.Time) (time.Time, bool) {
	body = strings.TrimSpace(body)
	if body == "" || !strings.HasPrefix(body, "{") {
		return time.Time{}, false
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return time.Time{}, false
	}
	if t, ok := walk(v, 0, received); ok {
		return t, true
	}
	return time.Time{}, false
}

func walk(v any, depth int, received time.Time) (time.Time, bool) {
	if depth > 3 {
		return time.Time{}, false
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return time.Time{}, false
	}
	for _, key := range []string{"reset", "reset_at", "resetAt"} {
		if n, ok := num(obj[key]); ok {
			return unixish(n, received), true
		}
	}
	for _, key := range []string{"retry_after", "retryAfter"} {
		if n, ok := num(obj[key]); ok && n < 1_000_000_000 {
			return received.Add(time.Duration(n) * time.Second), true
		}
	}
	for _, child := range obj {
		if t, ok := walk(child, depth+1, received); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func num(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
