package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/perkinsjr/limitcheck/internal/campaign"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func selftest(cmd spec.Command, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	results := make([]scenarioResult, 0, len(campaign.Scenarios()))
	failed := 0
	for _, sc := range campaign.Scenarios() {
		results = append(results, runScenario(ctx, sc))
		if results[len(results)-1].Err != "" {
			failed++
		}
	}
	if cmd.Input.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Fprintf(stderr, "limitcheck: %s\n", err)
			return 2
		}
	} else {
		for _, r := range results {
			state := "ok"
			if r.Err != "" {
				state = "FAIL"
			}
			fmt.Fprintf(stdout, "%-14s %s  %s\n", r.Name, state, r.Detail)
		}
		if failed == 0 {
			fmt.Fprintln(stdout, "self-test passed")
		} else {
			fmt.Fprintf(stdout, "self-test failed %d scenarios\n", failed)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func demo(cmd spec.Command, stdout, stderr io.Writer) int {
	name := campaign.Alias(cmd.Scenario)
	if name == "" {
		return selftest(cmd, stdout, stderr)
	}
	sc, ok := campaign.Find(name)
	if !ok {
		fmt.Fprintf(stderr, "limitcheck: unknown scenario %q\n", cmd.Scenario)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	report, err := campaign.Prove(ctx, sc)
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "scenario %s\n", sc.Name)
	if err := writeReport(stdout, cmd.Input.JSON, report); err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	return report.ExitCode()
}

type scenarioResult struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Err    string `json:"error,omitempty"`
}

func runScenario(ctx context.Context, sc campaign.Scenario) scenarioResult {
	report, err := campaign.Prove(ctx, sc)
	if err != nil {
		return scenarioResult{Name: sc.Name, Err: err.Error(), Detail: err.Error()}
	}
	var detail string
	for _, v := range report.Verdicts {
		if detail != "" {
			detail += "; "
		}
		detail += v.Name + " " + string(v.Status)
	}
	out := scenarioResult{Name: sc.Name, Detail: detail}
	if err := sc.Matches(report); err != nil {
		out.Err = err.Error()
		out.Detail = err.Error()
	}
	return out
}
