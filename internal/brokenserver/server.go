package brokenserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/perkinsjr/limitcheck/internal/hdr"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

type Server struct {
	URLs []string
	stop func()
}

func (s *Server) URL() string {
	if len(s.URLs) == 0 {
		return ""
	}
	return s.URLs[0]
}

// Close shuts the listeners down. It is safe to call twice.
func (s *Server) Close() {
	if s.stop != nil {
		s.stop()
	}
}

// Start listens on 127.0.0.1:0.
// algo is token, fixed, race, or a proof-only name
// (early, bad-headers, open, rotate, per-instance, shared).
func Start(rate spec.Rate, algo string) (*Server, error) {
	return Listen("127.0.0.1:0", rate, algo)
}

// Listen serves algo on addr. Pair algos ignore addr and use two ephemeral ports.
func Listen(addr string, rate spec.Rate, algo string) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	switch algo {
	case "token", "fixed", "race", "early", "bad-headers", "open", "rotate":
		return listenOne(addr, rate, algo)
	case "per-instance":
		return listenPair(rate, false)
	case "shared":
		return listenPair(rate, true)
	default:
		return nil, fmt.Errorf("unknown algo %q", algo)
	}
}

func listenOne(addr string, rate spec.Rate, algo string) (*Server, error) {
	h := handler{lim: makeLim(rate, algo), n: rate.Count, window: rate.Window, algo: algo}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	var once sync.Once
	return &Server{
		URLs: []string{"http://" + ln.Addr().String() + "/"},
		stop: func() { once.Do(func() { _ = srv.Shutdown(context.Background()) }) },
	}, nil
}

func listenPair(rate spec.Rate, shared bool) (*Server, error) {
	var a, b limiter
	if shared {
		one := newSliding(rate.Count, rate.Window)
		a, b = one, one
	} else {
		a = newSliding(rate.Count, rate.Window)
		b = newSliding(rate.Count, rate.Window)
	}
	var urls []string
	var srvs []*http.Server
	for _, lim := range []limiter{a, b} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		h := handler{lim: lim, n: rate.Count, window: rate.Window, algo: "token", sharedKey: shared}
		srv := &http.Server{Handler: h}
		go func() { _ = srv.Serve(ln) }()
		urls = append(urls, "http://"+ln.Addr().String()+"/")
		srvs = append(srvs, srv)
	}
	var once sync.Once
	return &Server{
		URLs: urls,
		stop: func() {
			once.Do(func() {
				for _, srv := range srvs {
					_ = srv.Shutdown(context.Background())
				}
			})
		},
	}, nil
}

func makeLim(rate spec.Rate, algo string) limiter {
	switch algo {
	case "fixed":
		return newFixed(rate.Count, rate.Window)
	case "race":
		return newRace(rate.Count, rate.Window)
	case "early":
		return newEarly(rate.Count, rate.Window)
	case "open":
		return newOpen(rate.Count, rate.Window)
	default:
		return newSliding(rate.Count, rate.Window)
	}
}

type handler struct {
	lim       limiter
	n         int
	window    time.Duration
	algo      string
	sharedKey bool
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	ok, rem, reset := h.lim.allow(h.key(r), now)
	shape := hdr.ShapeFull
	if !ok && h.algo == "bad-headers" {
		shape = hdr.ShapeWrong
	}
	hdr.Write(w, shape, hdr.Decision{
		Allowed: ok, Limit: h.n, Remaining: rem, Reset: reset, Window: h.window, Now: now,
	})
}

func (h handler) key(r *http.Request) string {
	if h.sharedKey {
		return "all"
	}
	id := r.Header.Get("X-Identity")
	if id == "" {
		id = "anon"
	}
	if h.algo == "rotate" {
		return foldRequestIDIntoKey(id, r)
	}
	return id
}

func foldRequestIDIntoKey(id string, r *http.Request) string {
	return id + "\n" + r.Header.Get("X-Limitcheck-Request")
}
