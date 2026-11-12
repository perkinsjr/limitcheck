// Package schedule fires a sealed plan at absolute times and records the results.
package schedule

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/safety"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// ErrDryRun means Execute was asked to send a dry run.
var ErrDryRun = errors.New("dry run sends nothing")

// Execute waits until each slot's fire time, sends the group that shares that
// time together, and records how late the send started. It does not pace with
// a fixed sleep loop. A slot whose time has already passed is sent immediately
// and the lateness is stored on the event.
func Execute(ctx context.Context, c spec.Campaign, p plan.Plan) (observe.Log, error) {
	if err := safety.Gate(c, p); err != nil {
		return observe.Log{}, err
	}
	if c.DryRun {
		return observe.Log{}, ErrDryRun
	}
	transport := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 128,
		IdleConnTimeout:     2 * time.Second,
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()

	events := make([]observe.Event, len(p.Slots))
	var wg sync.WaitGroup
	for i := 0; i < len(p.Slots); {
		j := i + 1
		for j < len(p.Slots) && p.Slots[j].FireAt.Equal(p.Slots[i].FireAt) {
			j++
		}
		if err := waitUntil(ctx, p.Slots[i].FireAt); err != nil {
			wg.Wait()
			return observe.Log{}, err
		}
		for k := i; k < j; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				events[k] = send(ctx, client, c, p.Slots[k])
			}(k)
		}
		i = j
	}
	wg.Wait()
	return observe.Log{
		Events:     events,
		BoundaryAt: p.BoundaryAt,
		Limit:      p.Limit,
	}, nil
}

func waitUntil(ctx context.Context, at time.Time) error {
	delay := time.Until(at)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func send(ctx context.Context, client *http.Client, c spec.Campaign, slot plan.Slot) observe.Event {
	ev := observe.Event{
		SlotID:   slot.ID,
		Tag:      slot.Tag,
		Origin:   slot.Origin,
		Identity: slot.Identity,
		FireAt:   slot.FireAt,
	}
	rawURL, err := spec.RequestURL(c, slot.Origin)
	if err != nil {
		ev.Err = err.Error()
		ev.Started = time.Now()
		ev.Ended = ev.Started
		return ev
	}
	var body io.Reader
	if c.Target.Body != "" {
		body = strings.NewReader(c.Target.Body)
	}
	req, err := http.NewRequestWithContext(ctx, c.Target.Method, rawURL, body)
	if err != nil {
		ev.Err = err.Error()
		ev.Started = time.Now()
		ev.Ended = ev.Started
		return ev
	}
	for _, h := range c.Target.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	if slot.Identity >= 0 && slot.Identity < len(c.Identities) {
		for _, h := range c.Identities[slot.Identity].Headers {
			req.Header.Set(h.Name, h.Value)
		}
	}
	req.Header.Set("User-Agent", "limitcheck")
	req.Header.Set("X-Limitcheck-Request", strconv.Itoa(slot.ID))
	if c.Target.Body != "" && req.Header.Get("Content-Type") == "" {
		trimmed := strings.TrimSpace(c.Target.Body)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	ev.Started = time.Now()
	resp, err := client.Do(req)
	ev.Ended = time.Now()
	if err != nil {
		ev.Err = err.Error()
		return ev
	}
	defer resp.Body.Close()
	ev.Status = resp.StatusCode
	ev.Header = resp.Header.Clone()
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if len(buf) > 512 {
		buf = buf[:512]
	}
	ev.Body = string(buf)
	return ev
}
