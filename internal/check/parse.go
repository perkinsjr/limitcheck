package check

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
)

var (
	reRemaining = regexp.MustCompile(`(?:^|[;,\s])r=(\d+)|remaining=(\d+)`)
	reQuota     = regexp.MustCompile(`(?:^|[;,\s])q=(\d+)|(?:^|[;,\s])limit=(\d+)`)
)

func remainingOf(h http.Header) (int, bool) {
	if h == nil {
		return 0, false
	}
	if raw := h.Get("X-RateLimit-Remaining"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil {
			return n, true
		}
	}
	if raw := h.Get("RateLimit-Remaining"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil {
			return n, true
		}
	}
	return firstNumber(reRemaining, h.Get("RateLimit"))
}

func limitOf(h http.Header) (int, bool) {
	if h == nil {
		return 0, false
	}
	if raw := h.Get("X-RateLimit-Limit"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil {
			return n, true
		}
	}
	if raw := h.Get("RateLimit-Limit"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil {
			return n, true
		}
	}
	if n, ok := firstNumber(reQuota, h.Get("RateLimit-Policy")); ok {
		return n, true
	}
	return firstNumber(reQuota, h.Get("RateLimit"))
}

func firstNumber(re *regexp.Regexp, raw string) (int, bool) {
	m := re.FindStringSubmatch(raw)
	if m == nil {
		return 0, false
	}
	for _, part := range m[1:] {
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func hasRateLimit(h http.Header) bool {
	if h == nil {
		return false
	}
	for _, name := range []string{"RateLimit", "RateLimit-Policy", "RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset"} {
		if h.Get(name) != "" {
			return true
		}
	}
	return false
}

func hasXRateLimit(h http.Header) bool {
	if h == nil {
		return false
	}
	for _, name := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if h.Get(name) != "" {
			return true
		}
	}
	return false
}

func limitsDisagree(h http.Header) (string, bool) {
	xRem, xRemOK := atoiHeader(h, "X-RateLimit-Remaining")
	stdRem, stdRemOK := remainingFromStandard(h)
	if xRemOK && stdRemOK && xRem != stdRem {
		return "X-RateLimit-Remaining and RateLimit disagree", true
	}
	xLim, xLimOK := atoiHeader(h, "X-RateLimit-Limit")
	stdLim, stdLimOK := limitFromStandard(h)
	if xLimOK && stdLimOK && xLim != stdLim {
		return "X-RateLimit-Limit and RateLimit-Policy disagree", true
	}
	return "", false
}

func atoiHeader(h http.Header, name string) (int, bool) {
	if h == nil {
		return 0, false
	}
	raw := strings.TrimSpace(h.Get(name))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

func remainingFromStandard(h http.Header) (int, bool) {
	if h == nil {
		return 0, false
	}
	if raw := h.Get("RateLimit-Remaining"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil {
			return n, true
		}
	}
	return firstNumber(reRemaining, h.Get("RateLimit"))
}

func limitFromStandard(h http.Header) (int, bool) {
	if h == nil {
		return 0, false
	}
	if raw := h.Get("RateLimit-Limit"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil {
			return n, true
		}
	}
	if n, ok := firstNumber(reQuota, h.Get("RateLimit-Policy")); ok {
		return n, true
	}
	return firstNumber(reQuota, h.Get("RateLimit"))
}

// deadline is when the server said the caller may try again.
// source is "absolute" for a timestamp and "retry-after" for a whole-second hint.
func deadline(e observe.Event) (time.Time, string, bool) {
	received := e.Ended
	if received.IsZero() {
		received = e.Started
	}
	if t, ok := bodyReset(e.Body, received); ok {
		return t, "absolute", true
	}
	if e.Header != nil {
		for _, name := range []string{"X-RateLimit-Reset", "RateLimit-Reset"} {
			if t, ok := parseReset(e.Header.Get(name), received); ok {
				return t, "absolute", true
			}
		}
	}
	if t, ok := retryAt(e); ok {
		return t, "retry-after", true
	}
	return time.Time{}, "", false
}

func retryAt(e observe.Event) (time.Time, bool) {
	if e.Header == nil {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(e.Header.Get("Retry-After"))
	if raw == "" {
		return time.Time{}, false
	}
	base := e.Ended
	if base.IsZero() {
		base = e.Started
	}
	if sec, err := strconv.Atoi(raw); err == nil && sec >= 0 {
		return base.Add(time.Duration(sec) * time.Second), true
	}
	if t, err := http.ParseTime(raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func parseReset(raw string, received time.Time) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	switch {
	case n > 1_000_000_000_000:
		return time.UnixMilli(n), true
	case n > 1_000_000_000:
		return time.Unix(n, 0), true
	default:
		return received.Add(time.Duration(n) * time.Second), true
	}
}

func bodyReset(body string, received time.Time) (time.Time, bool) {
	body = strings.TrimSpace(body)
	if body == "" || body[0] != '{' {
		return time.Time{}, false
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return time.Time{}, false
	}
	return findReset(v, received, 0)
}

func findReset(v any, received time.Time, depth int) (time.Time, bool) {
	if depth > 3 {
		return time.Time{}, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return time.Time{}, false
	}
	for _, key := range []string{"reset", "reset_at", "resetAt"} {
		if x, ok := m[key]; ok {
			if t, ok := asTime(x, received, true); ok {
				return t, true
			}
		}
	}
	for _, key := range []string{"retry_after", "retryAfter"} {
		if x, ok := m[key]; ok {
			if t, ok := asTime(x, received, false); ok {
				return t, true
			}
		}
	}
	for _, child := range m {
		if t, ok := findReset(child, received, depth+1); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func asTime(v any, received time.Time, absolute bool) (time.Time, bool) {
	var n float64
	switch t := v.(type) {
	case float64:
		n = t
	case string:
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts, true
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return time.Time{}, false
		}
		n = parsed
	default:
		return time.Time{}, false
	}
	if n > 1e12 {
		return time.UnixMilli(int64(n)), true
	}
	if n > 1e9 {
		return time.Unix(int64(n), 0), true
	}
	if absolute || n < 0 {
		return time.Time{}, false
	}
	return received.Add(time.Duration(n * float64(time.Second))), true
}
