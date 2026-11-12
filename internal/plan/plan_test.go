package plan

import (
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestBuildSchedule(t *testing.T) {
	c := campaign(t, []string{
		"http://127.0.0.1:8080/v1/items",
		"--limit", "5/1s",
		"--identity", "X-Identity: a",
		"--identity", "X-Identity: b",
	})
	now := time.Unix(1_700_000_000, 0)
	p, err := Build(c, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Build(c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Slots) != len(again.Slots) {
		t.Fatal("plan changed across two builds")
	}
	for i := range p.Slots {
		if p.Slots[i] != again.Slots[i] {
			t.Fatalf("slot %d changed", i)
		}
	}

	n := 5
	extra := 2
	// burst, second identity, early probe, late probe, pre, post, steady
	want := (n + burstExtra) + 1 + 1 + 1 + n + n + (n + extra)
	if len(p.Slots) != want {
		t.Fatalf("slots = %d, want %d", len(p.Slots), want)
	}
	if !p.WindowStart.After(now) {
		t.Fatal("window start should be in the future")
	}
	if p.WindowStart.UnixNano()%int64(time.Second) != 0 {
		t.Fatal("window start is not on a 1s boundary")
	}
	if p.BoundaryAt.IsZero() || p.BoundaryAt.UnixNano()%int64(time.Second) != 0 {
		t.Fatalf("boundary %s", p.BoundaryAt)
	}

	var burstAt time.Time
	var preMax, postMin time.Time
	var steady []time.Time
	burstN, preN, postN, probeN, steadyN := 0, 0, 0, 0, 0
	for i, s := range p.Slots {
		if i > 0 && s.FireAt.Before(p.Slots[i-1].FireAt) {
			t.Fatal("slots are not time ordered")
		}
		if !s.FireAt.After(now) {
			t.Fatal("slot is not in the future")
		}
		switch s.Tag {
		case spec.TagBurst:
			burstN++
			if burstAt.IsZero() {
				burstAt = s.FireAt
			} else if !s.FireAt.Equal(burstAt) {
				t.Fatal("burst slots do not share a fire time")
			}
		case spec.TagPreBoundary:
			preN++
			preMax = s.FireAt
		case spec.TagPostBoundary:
			postN++
			if postMin.IsZero() || s.FireAt.Before(postMin) {
				postMin = s.FireAt
			}
		case spec.TagProbe:
			probeN++
			if s.Identity == 1 && !s.FireAt.Before(p.WindowStart.Add(time.Second)) {
				t.Fatal("second identity is outside the burst window")
			}
		case spec.TagSteady:
			steadyN++
			steady = append(steady, s.FireAt)
		default:
			t.Fatalf("tag %s", s.Tag)
		}
	}
	if burstN != n+burstExtra || preN != n || postN != n || probeN != 3 || steadyN != n+extra {
		t.Fatalf("counts burst %d pre %d post %d probe %d steady %d", burstN, preN, postN, probeN, steadyN)
	}
	if !preMax.Before(p.BoundaryAt) || !postMin.After(p.BoundaryAt) {
		t.Fatalf("edge bursts do not straddle %s (%s .. %s)", p.BoundaryAt, preMax, postMin)
	}
	if p.BoundaryAt.Sub(preMax) < 50*time.Millisecond || postMin.Sub(p.BoundaryAt) < 20*time.Millisecond {
		t.Fatal("edge bursts sit too close to the boundary for timer slack")
	}
	if len(steady) < 2 {
		t.Fatal("steady run is too short")
	}
	for i := 1; i < len(steady); i++ {
		if !steady[i].After(steady[i-1]) {
			t.Fatal("steady slots are not spaced")
		}
	}
	steadyWindow := steady[0].Truncate(time.Second)
	if !steady[len(steady)-1].Before(steadyWindow.Add(time.Second)) {
		t.Fatal("steady run crosses a window edge")
	}
	// Post-boundary traffic owns the window that opens at BoundaryAt.
	// The steady run has to start in a later window.
	if !steadyWindow.Equal(p.BoundaryAt.Add(time.Second)) {
		t.Fatalf("steady window %s, boundary %s", steadyWindow, p.BoundaryAt)
	}
}

func TestBuildMultiOriginBurst(t *testing.T) {
	c := campaign(t, []string{
		"http://127.0.0.1:8080/v1/items",
		"--limit", "5/1s",
		"--origin", "http://127.0.0.1:8080",
		"--origin", "http://127.0.0.1:8081",
		"--check", "multi_instance,burst",
	})
	p, err := Build(c, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	perOrigin := map[int]int{}
	var at time.Time
	for _, s := range p.Slots {
		if s.Tag != spec.TagBurst {
			t.Fatalf("unexpected tag %s", s.Tag)
		}
		perOrigin[s.Origin]++
		if at.IsZero() {
			at = s.FireAt
		} else if !s.FireAt.Equal(at) {
			t.Fatal("origins do not fire together")
		}
	}
	if perOrigin[0] != 7 || perOrigin[1] != 7 {
		t.Fatalf("per origin %v", perOrigin)
	}
}

func TestBuildAccuracyOnlyIsOneWindow(t *testing.T) {
	c := campaign(t, []string{"http://127.0.0.1/x", "--limit", "5/1s", "--check", "accuracy"})
	now := time.Unix(1_700_000_000, 0)
	p, err := Build(c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Slots) != 7 {
		t.Fatalf("slots %d", len(p.Slots))
	}
	start := p.Slots[0].FireAt
	end := p.Slots[len(p.Slots)-1].FireAt
	if end.Sub(start) >= time.Second {
		t.Fatalf("accuracy run spans %s", end.Sub(start))
	}
	if !p.BoundaryAt.IsZero() {
		t.Fatal("accuracy plan should not schedule a boundary")
	}
}

func TestBuildRejectsShortWindow(t *testing.T) {
	c := campaign(t, []string{"http://127.0.0.1/x", "--limit", "5/100ms", "--check", "burst"})
	if _, err := Build(c, time.Now()); err == nil {
		t.Fatal("short window should fail")
	}
}

func campaign(t *testing.T, args []string) spec.Campaign {
	t.Helper()
	cmd, err := spec.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Campaign
}
