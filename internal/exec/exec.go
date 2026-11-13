// Package exec sends each stamped slot once. Transport errors are observations.
package exec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/perkinsjr/limitcheck/internal/hdr"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Client is the HTTP client for a run. Redirects are not followed.
func Client(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = spec.DefaultPerRequest
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{MaxIdleConnsPerHost: spec.DefaultMaxInFlight},
	}
}

// Send fires a planned wave. One timer per distinct grid time. No retries.
func Send(ctx context.Context, in spec.Input, client *http.Client, p sched.Planned, checkName string) ([]observe.Event, error) {
	if len(p.Slots) == 0 {
		return nil, nil
	}
	inflight := p.Wave.InFlight
	if inflight < 1 {
		inflight = 1
	}
	if inflight > in.Caps.MaxInFlight && in.Caps.MaxInFlight > 0 {
		inflight = in.Caps.MaxInFlight
	}
	sem := make(chan struct{}, inflight)
	events := make([]observe.Event, len(p.Slots))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < len(p.Slots); {
		at := p.Slots[i].GridAt
		j := i + 1
		for j < len(p.Slots) && p.Slots[j].GridAt.Equal(at) {
			j++
		}
		if err := waitUntil(ctx, at); err != nil {
			wg.Wait()
			return nil, err
		}
		for k := i; k < j; k++ {
			slot := p.Slots[k]
			if !slot.NotAfter.IsZero() && !time.Now().Before(slot.NotAfter) {
				events[k] = held(slot, checkName)
				continue
			}
			wg.Add(1)
			go func(k int, slot sched.Slot) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				ev := sendOne(ctx, in, client, slot, checkName)
				mu.Lock()
				events[k] = ev
				mu.Unlock()
			}(k, slot)
		}
		i = j
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return events, nil
}

func held(slot sched.Slot, checkName string) observe.Event {
	return observe.Event{
		Check: checkName, Wave: slot.Wave, Marker: slot.Marker,
		Origin: slot.Origin, Identity: slot.Identity,
		GridAt: slot.GridAt, Edge: slot.Edge, Held: true,
	}
}

func waitUntil(ctx context.Context, at time.Time) error {
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sendOne(ctx context.Context, in spec.Input, client *http.Client, slot sched.Slot, checkName string) observe.Event {
	ev := observe.Event{
		Check: checkName, Wave: slot.Wave, Marker: slot.Marker,
		Origin: slot.Origin, Identity: slot.Identity,
		GridAt: slot.GridAt, Edge: slot.Edge,
	}
	if slot.Origin < 0 || slot.Origin >= len(in.Origins) {
		ev.Err = "origin index out of range"
		ev.SentAt = time.Now()
		return ev
	}
	method := in.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if in.Body != "" {
		body = strings.NewReader(in.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, in.Origins[slot.Origin].URL(), body)
	if err != nil {
		ev.Err = err.Error()
		ev.SentAt = time.Now()
		return ev
	}
	applyHeaders(req, in, slot)
	ev.SentAt = time.Now()
	res, err := client.Do(req)
	if err != nil {
		ev.Err = err.Error()
		return ev
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	ev.Status = res.StatusCode
	ev.Reading = hdr.Interpret(res.StatusCode, res.Header, string(buf), ev.SentAt)
	return ev
}

func applyHeaders(req *http.Request, in spec.Input, slot sched.Slot) {
	for _, h := range in.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	if slot.Identity >= 0 && slot.Identity < len(in.Identities) {
		for _, h := range in.Identities[slot.Identity].Headers {
			req.Header.Set(h.Name, h.Value)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "limitcheck")
	}
	if req.Header.Get("X-Limitcheck-Request") == "" {
		req.Header.Set("X-Limitcheck-Request", fmt.Sprintf("%s-%d-%d", slot.Wave, slot.Origin, slot.Index))
	}
	if in.Body != "" && req.Header.Get("Content-Type") == "" {
		b := strings.TrimSpace(in.Body)
		if strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[") {
			req.Header.Set("Content-Type", "application/json")
		}
	}
}
