package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDryRunPrintsPlan(t *testing.T) {
	var out, err bytes.Buffer
	code := run([]string{"http://127.0.0.1:1/check", "--limit", "5/1s", "--dry-run", "--check", "burst"}, &out, &err)
	if code != 0 {
		t.Fatalf("code %d stderr %s", code, err.String())
	}
	if !strings.Contains(out.String(), "nothing sent") {
		t.Fatal(out.String())
	}
}

func TestDryRunRemoteSkipsTheGuard(t *testing.T) {
	var out, err bytes.Buffer
	start := time.Now()
	code := run([]string{"--dry-run", "--limit", "5/1s", "--check", "burst", "http://203.0.113.5/items"}, &out, &err)
	if code != 0 {
		t.Fatalf("code %d %s", code, err.String())
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("dry run took too long")
	}
	if !strings.Contains(out.String(), "nothing sent") || err.Len() != 0 {
		t.Fatalf("out %s err %s", out.String(), err.String())
	}
}

func TestRemoteWithoutYesRefuses(t *testing.T) {
	var out, err bytes.Buffer
	start := time.Now()
	code := run([]string{"http://203.0.113.5/items", "--limit", "5/1s"}, &out, &err)
	if code != 2 {
		t.Fatalf("code %d out %s err %s", code, out.String(), err.String())
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("refusal dialed")
	}
	if !strings.Contains(err.String(), "--yes") || out.Len() != 0 {
		t.Fatalf("out %q err %q", out.String(), err.String())
	}
}

func TestMaxRequestsCapsThePlan(t *testing.T) {
	var out, err bytes.Buffer
	code := run([]string{"--dry-run", "--limit", "5/1s", "--max-requests", "1", "--check", "burst", "http://127.0.0.1/check"}, &out, &err)
	if code != 2 || !strings.Contains(err.String(), "max") {
		t.Fatalf("code %d err %s", code, err.String())
	}
}

func TestSelfTest(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	var out, err bytes.Buffer
	code := run([]string{"--self-test"}, &out, &err)
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out.String(), err.String())
	}
	text := out.String()
	for _, name := range []string{"token", "fixed", "race", "per-instance", "bad-headers", "self-test passed"} {
		if !strings.Contains(text, name) {
			t.Fatal(text)
		}
	}
}
