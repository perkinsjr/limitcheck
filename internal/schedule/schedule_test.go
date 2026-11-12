package schedule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestExecuteFiresBurstTogether(t *testing.T) {
	var mu sync.Mutex
	var got []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustCampaign(t, srv.URL+"/check", "5/1s", "burst")
	p, err := plan.Build(c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	log, err := Execute(context.Background(), c, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Events) != len(p.Slots) {
		t.Fatalf("events %d slots %d", len(log.Events), len(p.Slots))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(p.Slots) {
		t.Fatalf("server saw %d requests", len(got))
	}
	min, max := got[0], got[0]
	for _, ts := range got {
		if ts.Before(min) {
			min = ts
		}
		if ts.After(max) {
			max = ts
		}
	}
	if max.Sub(min) > 80*time.Millisecond {
		t.Fatalf("burst spread across %s", max.Sub(min))
	}
	for _, e := range log.Events {
		if e.Err != "" || e.Status != 200 {
			t.Fatalf("event %+v", e)
		}
		if e.Drift() < 0 {
			t.Fatalf("negative drift %s", e.Drift())
		}
		if e.Drift() > 200*time.Millisecond {
			t.Fatalf("drift %s", e.Drift())
		}
	}
}

func TestExecuteDryRunSendsNothing(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := mustCampaign(t, srv.URL+"/check", "5/1s", "burst")
	c.DryRun = true
	p, err := plan.Build(c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Execute(context.Background(), c, p)
	if err != ErrDryRun {
		t.Fatalf("err %v", err)
	}
	if hits != 0 {
		t.Fatalf("dry run sent %d requests", hits)
	}
}

func TestExecuteRefusesRemoteWithoutYes(t *testing.T) {
	cmd, err := spec.Parse([]string{
		"http://203.0.113.5/items",
		"--limit", "5/1s",
		"--max-requests", "100",
		"--check", "burst",
	})
	if err != nil {
		t.Fatal(err)
	}
	c := cmd.Campaign
	p, err := plan.Build(c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Execute(ctx, c, p)
	if err == nil {
		t.Fatal("remote target should be refused")
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Fatal("refusal waited on the network")
	}
}

func mustCampaign(t *testing.T, rawURL, limit, checks string) spec.Campaign {
	t.Helper()
	cmd, err := spec.Parse([]string{
		rawURL,
		"--limit", limit,
		"--max-requests", "100",
		"--check", checks,
		"--yes",
	})
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Campaign
}
