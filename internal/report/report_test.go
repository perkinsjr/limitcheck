package report

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestTextIncludesVerdictAndTimeline(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	log := observe.Log{Events: []observe.Event{
		{Tag: spec.TagSteady, Status: 200, Started: start, Ended: start.Add(time.Millisecond)},
		{Tag: spec.TagSteady, Status: 429, Started: start.Add(20 * time.Millisecond), Ended: start.Add(21 * time.Millisecond)},
	}}
	vs := []check.Verdict{{
		Kind:    spec.CheckAccuracy,
		Status:  check.Fail,
		Message: "steady run allowed 7 requests, limit is 5",
	}}
	u, err := url.Parse("http://127.0.0.1/check")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	WriteText(&buf, spec.Campaign{
		Target: spec.Target{URL: u},
		Limit:  spec.Limit{Count: 5, Window: time.Second},
	}, log, vs)
	out := buf.String()
	if !strings.Contains(out, "accuracy") || !strings.Contains(out, "fail") {
		t.Fatalf("verdict missing\n%s", out)
	}
	if !strings.Contains(out, "1 check failed") {
		t.Fatalf("summary missing\n%s", out)
	}
	if !strings.Contains(out, "+") || !strings.Contains(out, "x") {
		t.Fatalf("timeline missing\n%s", out)
	}
	if ExitCode(vs) != 1 {
		t.Fatal("exit")
	}
	if ExitCode(nil) != 0 {
		t.Fatal("empty exit")
	}
}

func TestDryRunSaysNothingSent(t *testing.T) {
	var buf bytes.Buffer
	WriteDryRun(&buf, spec.Campaign{
		Limit:      spec.Limit{Count: 1, Window: time.Second},
		Identities: []spec.Identity{{Name: "default"}},
	}, plan.Plan{Slots: []plan.Slot{{
		FireAt: time.Unix(1_700_000_000, 0),
		Tag:    spec.TagBurst,
	}}})
	if !strings.Contains(buf.String(), "nothing sent") {
		t.Fatalf("%s", buf.String())
	}
}

func TestJSONMarksFailure(t *testing.T) {
	var buf bytes.Buffer
	err := WriteJSON(&buf, spec.Campaign{Limit: spec.Limit{Count: 5, Window: time.Second}}, observe.Log{}, []check.Verdict{{
		Kind: spec.CheckBurst, Status: check.Fail, Message: "concurrent burst allowed 7 requests, limit is 5",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"ok": false`) || !strings.Contains(buf.String(), `"failed": 1`) {
		t.Fatalf("%s", buf.String())
	}
}
