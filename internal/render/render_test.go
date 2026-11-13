package render

import (
	"bytes"
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestFailExitAndTimeline(t *testing.T) {
	r := Report{
		URL:   "http://127.0.0.1/check",
		Limit: spec.Rate{Count: 5, Window: time.Second},
		Verdicts: []check.Verdict{
			{Name: "accuracy", Status: check.Fail, Message: "steady run allowed 9 requests, limit is 5"},
			{Name: "burst", Status: check.Pass, Message: "ok"},
		},
		Events: []observe.Event{
			{SentAt: time.Unix(0, 0), Status: 200},
			{SentAt: time.Unix(0, int64(time.Millisecond)), Status: 429},
		},
	}
	if r.ExitCode() != 1 {
		t.Fatal(r.ExitCode())
	}
	var buf bytes.Buffer
	WriteText(&buf, r)
	text := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("accuracy")) || !bytes.Contains(buf.Bytes(), []byte("1 check failed")) {
		t.Fatal(text)
	}
	if !bytes.Contains(buf.Bytes(), []byte("+")) || !bytes.Contains(buf.Bytes(), []byte("x")) {
		t.Fatal(text)
	}
	buf.Reset()
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"ok": false`)) || !bytes.Contains(buf.Bytes(), []byte(`"failed": 1`)) {
		t.Fatal(buf.String())
	}
}

func TestDryRunAndInconclusive(t *testing.T) {
	dry := Report{DryRun: true, Planned: 7, Limit: spec.Rate{Count: 5, Window: time.Second}, Waves: []WaveLine{{Check: "burst", Name: "burst", Count: 7}}}
	if dry.ExitCode() != 0 {
		t.Fatal(dry.ExitCode())
	}
	var buf bytes.Buffer
	WriteText(&buf, dry)
	if !bytes.Contains(buf.Bytes(), []byte("nothing sent")) {
		t.Fatal(buf.String())
	}
	inc := Report{Verdicts: []check.Verdict{{Name: "boundary", Status: check.Inconclusive}}}
	if inc.ExitCode() != 2 {
		t.Fatal(inc.ExitCode())
	}
}
