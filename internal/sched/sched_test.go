package sched

import (
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestStampEdgeStraddlesOneBoundary(t *testing.T) {
	window := time.Second
	now := time.Now()
	group := []Wave{
		{Name: "pre", Count: 3, InFlight: 3, Offset: -100 * time.Millisecond, Anchor: "edge", Gate: spec.Gate{Kind: spec.SnapshotReset, MustHit: true}},
		{Name: "post", Count: 3, InFlight: 3, Offset: 40 * time.Millisecond, Anchor: "edge", Gate: spec.Gate{Kind: spec.SnapshotReset, MustHit: true}},
	}
	planned := StampEdge(group, now, window, 1)
	if planned[0].Held || planned[1].Held {
		t.Fatal("held a future edge")
	}
	pre := planned[0].Slots[0].GridAt
	post := planned[1].Slots[0].GridAt
	if !pre.Before(post) {
		t.Fatal("pre is not before post")
	}
	boundary := pre.Add(100 * time.Millisecond)
	if !post.Equal(boundary.Add(40 * time.Millisecond)) {
		t.Fatalf("post %s boundary %s", post, boundary)
	}
	if !pre.After(now) {
		t.Fatal("pre is not in the future")
	}
	if pre.Sub(now) < window {
		t.Fatal("edge is inside the previous window")
	}
	if planned[0].Slots[0].NotAfter.IsZero() {
		t.Fatal("pre has no hold deadline")
	}
}

func TestCostCountsFanout(t *testing.T) {
	waves := []Wave{{Count: 4, Fanout: true}, {Count: 1}}
	if got := Cost(waves, 2); got != 9 {
		t.Fatal(got)
	}
}

func TestAdmitSkipsNothingOnRemote(t *testing.T) {
	in := spec.Input{Origins: []spec.Origin{{Host: "203.0.113.5"}}}
	if err := Admit(in); err == nil {
		t.Fatal("remote without yes")
	}
	in.Yes = true
	if err := Admit(in); err != nil {
		t.Fatal(err)
	}
	local := spec.Input{Origins: []spec.Origin{{Host: "127.0.0.1:9"}}}
	if err := Admit(local); err != nil {
		t.Fatal(err)
	}
}

func TestReadyIsACleanWindowAhead(t *testing.T) {
	now := time.Now()
	window := 500 * time.Millisecond
	p := Stamp(Wave{Name: "steady", Count: 2, InFlight: 1, Interval: 20 * time.Millisecond, Gate: spec.Gate{Kind: spec.Ready}}, now, window, 1)
	if !p.Slots[0].GridAt.After(now.Add(window)) {
		t.Fatalf("grid %s now %s", p.Slots[0].GridAt, now)
	}
	if !p.Slots[1].GridAt.After(p.Slots[0].GridAt) {
		t.Fatal("interval collapsed")
	}
}
