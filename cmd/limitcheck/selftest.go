package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/report"
	"github.com/perkinsjr/limitcheck/internal/safety"
	"github.com/perkinsjr/limitcheck/internal/schedule"
	"github.com/perkinsjr/limitcheck/internal/spec"
	"github.com/perkinsjr/limitcheck/internal/testserver"
)

type expectation struct {
	kind   spec.CheckKind
	status check.Status
	text   string
}

// expectations are what each broken limiter must produce.
// correct has to pass. The others have to fail the check that matches the bug.
var expectations = map[string][]expectation{
	testserver.ModeCorrect: {
		{spec.CheckAccuracy, check.Pass, ""},
		{spec.CheckBoundary, check.Pass, ""},
		{spec.CheckBurst, check.Pass, ""},
		{spec.CheckIsolation, check.Pass, ""},
		{spec.CheckHeaders, check.Pass, ""},
		{spec.CheckRecovery, check.Pass, ""},
	},
	testserver.ModeFixed: {
		{spec.CheckBoundary, check.Fail, "window edge"},
		{spec.CheckAccuracy, check.Pass, ""},
		{spec.CheckBurst, check.Pass, ""},
		{spec.CheckIsolation, check.Pass, ""},
		{spec.CheckHeaders, check.Pass, ""},
		{spec.CheckRecovery, check.Pass, ""},
	},
	testserver.ModeRacy: {
		{spec.CheckBurst, check.Fail, "concurrent burst"},
	},
	testserver.ModePerInstance: {
		{spec.CheckMultiInstance, check.Fail, "its own"},
		{spec.CheckBurst, check.Pass, ""},
		{spec.CheckIsolation, check.Pass, ""},
		{spec.CheckBoundary, check.Pass, ""},
		{spec.CheckAccuracy, check.Pass, ""},
	},
	testserver.ModeShared: {
		{spec.CheckMultiInstance, check.Pass, "shared"},
		{spec.CheckIsolation, check.Fail, "second identity"},
		{spec.CheckAccuracy, check.Pass, ""},
		{spec.CheckBurst, check.Pass, ""},
	},
	testserver.ModeEarly: {
		{spec.CheckRecovery, check.Fail, "before the advertised reset"},
		{spec.CheckBurst, check.Pass, ""},
		{spec.CheckAccuracy, check.Pass, ""},
		{spec.CheckBoundary, check.Pass, ""},
	},
	testserver.ModeBadHeaders: {
		{spec.CheckHeaders, check.Fail, "429"},
		{spec.CheckAccuracy, check.Pass, ""},
		{spec.CheckBurst, check.Pass, ""},
		{spec.CheckBoundary, check.Pass, ""},
	},
	testserver.ModeOpen: {
		{spec.CheckAccuracy, check.Fail, "steady run allowed"},
	},
	testserver.ModeRotate: {
		{spec.CheckIsolation, check.Fail, "X-Limitcheck-Request"},
	},
}

type scenarioRun struct {
	Name     string          `json:"scenario"`
	OK       bool            `json:"ok"`
	Detail   string          `json:"detail"`
	Verdicts []check.Verdict `json:"verdicts,omitempty"`
}

func selftest(jsonOut bool, stdout, stderr io.Writer) int {
	var runs []scenarioRun
	failed := 0
	for _, name := range testserver.ScenarioNames() {
		run, err := proveScenario(name)
		if err != nil {
			run = scenarioRun{Name: name, Detail: err.Error()}
		}
		runs = append(runs, run)
		if !run.OK {
			failed++
		}
		if !jsonOut {
			state := "ok"
			if !run.OK {
				state = "not ok"
			}
			fmt.Fprintf(stdout, "%-14s %s  %s\n", name, state, run.Detail)
		}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(runs); err != nil {
			fmt.Fprintf(stderr, "limitcheck: %s\n", err)
			return 2
		}
	} else if failed == 0 {
		fmt.Fprintln(stdout, "self-test passed")
	} else {
		fmt.Fprintf(stdout, "self-test failed %d scenarios\n", failed)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func demo(scenario string, jsonOut bool, stdout, stderr io.Writer) int {
	if scenario == "" {
		return selftest(jsonOut, stdout, stderr)
	}
	if _, ok := expectations[scenario]; !ok {
		fmt.Fprintf(stderr, "limitcheck: unknown scenario %q\n", scenario)
		return 2
	}
	c, log, err := runScenario(scenario)
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	verdicts := check.Run(c, log)
	if jsonOut {
		if err := report.WriteJSON(stdout, c, log, verdicts); err != nil {
			fmt.Fprintf(stderr, "limitcheck: %s\n", err)
			return 2
		}
	} else {
		fmt.Fprintf(stdout, "scenario %s\n", scenario)
		report.WriteText(stdout, c, log, verdicts)
	}
	return report.ExitCode(verdicts)
}

func proveScenario(name string) (scenarioRun, error) {
	c, log, err := runScenario(name)
	if err != nil {
		return scenarioRun{}, err
	}
	verdicts := check.Run(c, log)
	ok, detail := matchExpectations(name, verdicts)
	return scenarioRun{Name: name, OK: ok, Detail: detail, Verdicts: verdicts}, nil
}

func runScenario(name string) (spec.Campaign, observe.Log, error) {
	running, err := testserver.StartScenario(name, 5, time.Second)
	if err != nil {
		return spec.Campaign{}, observe.Log{}, err
	}
	defer running.Close()
	c, err := demoCampaign(running.URLs)
	if err != nil {
		return spec.Campaign{}, observe.Log{}, err
	}
	p, err := plan.Build(c, time.Now())
	if err != nil {
		return spec.Campaign{}, observe.Log{}, err
	}
	if err := safety.Gate(c, p); err != nil {
		return spec.Campaign{}, observe.Log{}, err
	}
	log, err := schedule.Execute(context.Background(), c, p)
	if err != nil {
		return spec.Campaign{}, observe.Log{}, err
	}
	return c, log, nil
}

func demoCampaign(urls []string) (spec.Campaign, error) {
	if len(urls) == 0 {
		return spec.Campaign{}, fmt.Errorf("scenario has no url")
	}
	target := strings.TrimRight(urls[0], "/") + "/check"
	args := []string{
		target,
		"--limit", "5/1s",
		"--max-requests", "500",
		"--yes",
		"--identity", "name=a; X-Identity: a",
		"--identity", "name=b; X-Identity: b",
	}
	checks := "accuracy,boundary,burst,isolation,headers,recovery"
	if len(urls) >= 2 {
		for _, u := range urls {
			args = append(args, "--origin", u)
		}
		checks += ",multi_instance"
	}
	args = append(args, "--check", checks)
	cmd, err := spec.Parse(args)
	if err != nil {
		return spec.Campaign{}, err
	}
	return cmd.Campaign, nil
}

func matchExpectations(name string, vs []check.Verdict) (bool, string) {
	wants := expectations[name]
	by := map[spec.CheckKind]check.Verdict{}
	for _, v := range vs {
		by[v.Kind] = v
	}
	var bad []string
	var notes []string
	for _, want := range wants {
		got, ok := by[want.kind]
		if !ok {
			bad = append(bad, string(want.kind)+" missing")
			continue
		}
		if got.Status != want.status {
			bad = append(bad, fmt.Sprintf("%s was %s, want %s (%s)", want.kind, got.Status, want.status, got.Message))
			continue
		}
		if want.text != "" && !strings.Contains(got.Message, want.text) {
			bad = append(bad, fmt.Sprintf("%s message %q", want.kind, got.Message))
			continue
		}
		notes = append(notes, fmt.Sprintf("%s %s", want.kind, got.Status))
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, strings.Join(notes, ", ")
}
