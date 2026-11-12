// Package testserver hosts limiters that fail in known ways.
package testserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	ModeCorrect     = "correct"
	ModeFixed       = "fixed-window"
	ModeRacy        = "racy"
	ModePerInstance = "per-instance"
	ModeShared      = "shared"
	ModeEarly       = "early"
	ModeBadHeaders  = "bad-headers"
	ModeOpen        = "open"
	ModeRotate      = "rotate"
)

// ScenarioNames is the demo order.
func ScenarioNames() []string {
	return []string{
		ModeCorrect,
		ModeFixed,
		ModeRacy,
		ModePerInstance,
		ModeShared,
		ModeEarly,
		ModeBadHeaders,
		ModeOpen,
		ModeRotate,
	}
}

// Running is one or two local servers. Close is safe to call twice.
type Running struct {
	URLs  []string
	Close func()
}

// StartScenario listens on loopback. per-instance and shared use two ports.
func StartScenario(mode string, n int, window time.Duration) (*Running, error) {
	if err := known(mode); err != nil {
		return nil, err
	}
	if mode == ModePerInstance || mode == ModeShared {
		u1, u2, closeFn, err := startPair(mode, n, window)
		if err != nil {
			return nil, err
		}
		return &Running{URLs: []string{u1, u2}, Close: closeFn}, nil
	}
	u, closeFn, err := startOne(mode, newLimiter(mode, n, window), n, window)
	if err != nil {
		return nil, err
	}
	return &Running{URLs: []string{u}, Close: closeFn}, nil
}

func known(mode string) error {
	switch mode {
	case ModeCorrect, ModeFixed, ModeRacy, ModePerInstance, ModeShared, ModeEarly, ModeBadHeaders, ModeOpen, ModeRotate:
		return nil
	default:
		return fmt.Errorf("unknown scenario %q", mode)
	}
}

func startPair(mode string, n int, window time.Duration) (string, string, func(), error) {
	var a, b limiter
	if mode == ModePerInstance {
		a = newSliding(n, window)
		b = newSliding(n, window)
	} else {
		shared := newSliding(n, window)
		a, b = shared, shared
	}
	u1, c1, err := startOne(mode, a, n, window)
	if err != nil {
		return "", "", nil, err
	}
	u2, c2, err := startOne(mode, b, n, window)
	if err != nil {
		c1()
		return "", "", nil, err
	}
	var once sync.Once
	return u1, u2, func() { once.Do(func() { c1(); c2() }) }, nil
}

func startOne(mode string, lim limiter, n int, window time.Duration) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{
		Handler:           &handler{mode: mode, lim: lim, n: n, window: window},
		ReadHeaderTimeout: 2 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
	}
	return "http://" + ln.Addr().String(), closeFn, nil
}

type handler struct {
	mode   string
	lim    limiter
	n      int
	window time.Duration
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	now := time.Now()
	allowed, remaining, reset := h.lim.Take(h.key(r), now)
	if h.mode == ModeBadHeaders {
		if !allowed {
			http.Error(w, "slow down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
		return
	}
	writeLimited(w, h.n, h.window, allowed, remaining, reset, now)
}

func (h *handler) key(r *http.Request) string {
	switch h.mode {
	case ModeShared:
		return "all"
	case ModeRotate:
		// Every request carries a fresh X-Limitcheck-Request. Folding that into
		// the key makes header rotation look like a new client.
		return r.Header.Get("X-Identity") + "\n" + r.Header.Get("X-Limitcheck-Request")
	default:
		id := r.Header.Get("X-Identity")
		if id == "" {
			id = "anon"
		}
		return id
	}
}

func writeLimited(w http.ResponseWriter, n int, window time.Duration, allowed bool, remaining int, reset, now time.Time) {
	sec := retrySeconds(reset, now)
	w.Header().Set("Retry-After", strconv.Itoa(sec))
	w.Header().Set("RateLimit", fmt.Sprintf(`"default";r=%d;t=%d`, remaining, sec))
	w.Header().Set("RateLimit-Policy", fmt.Sprintf(`"default";q=%d;w=%d`, n, wholeSeconds(window)))
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(n))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]int64{"reset": reset.Unix()})
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func retrySeconds(reset, now time.Time) int {
	sec := int(math.Ceil(reset.Sub(now).Seconds()))
	if sec < 1 {
		return 1
	}
	return sec
}

func wholeSeconds(w time.Duration) int {
	sec := int(math.Round(w.Seconds()))
	if sec < 1 {
		return 1
	}
	return sec
}
