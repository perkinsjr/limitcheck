package testserver

import (
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestFixedWindowLeaksAcrossEdge(t *testing.T) {
	window := time.Second
	lim := newFixed(5, window)
	edge := time.Unix(0, window.Nanoseconds())
	pre := edge.Add(-10 * time.Millisecond)
	for i := 0; i < 5; i++ {
		ok, _, _ := lim.Take("a", pre)
		if !ok {
			t.Fatal("pre-edge request was rejected")
		}
	}
	post := edge.Add(10 * time.Millisecond)
	allowed := 0
	for i := 0; i < 5; i++ {
		ok, _, _ := lim.Take("a", post)
		if ok {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("fixed window allowed %d just after the edge, want 5", allowed)
	}

	slide := newSliding(5, window)
	for i := 0; i < 5; i++ {
		ok, _, _ := slide.Take("a", pre)
		if !ok {
			t.Fatal("sliding pre-edge request was rejected")
		}
	}
	allowed = 0
	for i := 0; i < 5; i++ {
		ok, _, _ := slide.Take("a", post)
		if ok {
			allowed++
		}
	}
	if allowed != 0 {
		t.Fatalf("sliding window allowed %d just after the edge, want 0", allowed)
	}
}

func TestRacyBurstAllowsMoreThanLimit(t *testing.T) {
	lim := newRacy(5, time.Second)
	now := time.Unix(50, 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 7; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, _ := lim.Take("a", now)
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed <= 5 {
		t.Fatalf("racy burst allowed %d, want more than 5", allowed)
	}

	// A later lone request must see the counter, not another free pass.
	ok, _, _ := lim.Take("a", now.Add(80*time.Millisecond))
	if ok {
		t.Fatal("request after the burst was allowed")
	}
}

func TestEarlyAllowsBeforeReset(t *testing.T) {
	window := time.Second
	lim := newEarly(5, window)
	start := time.Unix(80, 0)
	for i := 0; i < 5; i++ {
		ok, _, _ := lim.Take("a", start)
		if !ok {
			t.Fatal("fill request was rejected")
		}
	}
	ok, _, reset := lim.Take("a", start)
	if ok {
		t.Fatal("the immediate extra request was allowed")
	}
	if !reset.After(start.Add(500 * time.Millisecond)) {
		t.Fatalf("reset %s is not the window edge", reset)
	}
	ok, _, _ = lim.Take("a", start.Add(80*time.Millisecond))
	if !ok {
		t.Fatal("early limiter did not let a request through before the reset")
	}
}

func TestSharedPairCountsOnce(t *testing.T) {
	u1, u2, closeFn, err := startPair(ModeShared, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	client := &http.Client{Timeout: time.Second}
	var first, second int
	for i := 0; i < 3; i++ {
		if code := get(t, client, u1); code == 200 {
			first++
		}
	}
	for i := 0; i < 3; i++ {
		if code := get(t, client, u2); code == 200 {
			second++
		}
	}
	if first+second != 3 {
		t.Fatalf("shared origins allowed %d, want 3", first+second)
	}
}

func TestPerInstancePairCountsTwice(t *testing.T) {
	u1, u2, closeFn, err := startPair(ModePerInstance, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	client := &http.Client{Timeout: time.Second}
	a, b := 0, 0
	for i := 0; i < 3; i++ {
		if get(t, client, u1) == 200 {
			a++
		}
		if get(t, client, u2) == 200 {
			b++
		}
	}
	if a != 3 || b != 3 {
		t.Fatalf("per-instance allowed %d and %d, want 3 and 3", a, b)
	}
}

func TestCorrectResponseShape(t *testing.T) {
	running, err := StartScenario(ModeCorrect, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get(running.URLs[0])
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("remaining %q", res.Header.Get("X-RateLimit-Remaining"))
	}
	res, err = client.Get(running.URLs[0])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get("Retry-After") == "" || res.Header.Get("RateLimit") == "" {
		t.Fatalf("headers %v", res.Header)
	}
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("body %q", body)
	}
}

func TestBadHeadersUse503(t *testing.T) {
	running, err := StartScenario(ModeBadHeaders, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	client := &http.Client{Timeout: time.Second}
	if get(t, client, running.URLs[0]) != 200 {
		t.Fatal("first request")
	}
	res, err := client.Get(running.URLs[0])
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get("Retry-After") != "" {
		t.Fatal("bad limiter should not send Retry-After")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	running, err := StartScenario(ModeCorrect, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	running.Close()
	running.Close()
}

func get(t *testing.T, client *http.Client, rawURL string) int {
	t.Helper()
	res, err := client.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}
