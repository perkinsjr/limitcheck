package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDryRunPrintsPlan(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"http://127.0.0.1:1/check",
		"--limit", "5/1s",
		"--dry-run",
		"--check", "burst",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "nothing sent") {
		t.Fatalf("stdout %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr %s", stderr.String())
	}
}

func TestRemoteWithoutYesRefuses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"http://203.0.113.5/items", "--limit", "5/1s"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code %d stdout %s stderr %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("stderr %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should stay empty, got %s", stdout.String())
	}
}

func TestMaxRequestsCapsThePlan(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"http://127.0.0.1/check",
		"--limit", "5/1s",
		"--max-requests", "1",
		"--dry-run",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code %d stdout %s stderr %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "max") {
		t.Fatalf("stderr %s", stderr.String())
	}
}

func TestSelfTest(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--self-test"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "self-test passed") {
		t.Fatalf("stdout %s", out)
	}
	for _, name := range []string{"correct", "fixed-window", "racy", "per-instance", "bad-headers"} {
		if !strings.Contains(out, name) {
			t.Fatalf("missing %s\n%s", name, out)
		}
	}
}
