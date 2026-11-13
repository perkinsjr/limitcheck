package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/report"
	"github.com/perkinsjr/limitcheck/internal/safety"
	"github.com/perkinsjr/limitcheck/internal/schedule"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd, err := spec.Parse(args)
	if err != nil {
		if errors.Is(err, spec.ErrHelp) {
			fmt.Fprint(stdout, spec.Usage())
			return 0
		}
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	switch cmd.Action {
	case spec.ActionSelfTest:
		return selftest(cmd.Campaign.JSON, stdout, stderr)
	case spec.ActionDemo:
		return demo(cmd.Scenario, cmd.Campaign.JSON, stdout, stderr)
	default:
		return execute(cmd.Campaign, stdout, stderr)
	}
}

func execute(c spec.Campaign, stdout, stderr io.Writer) int {
	p, err := plan.Build(c, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	if err := safety.Gate(c, p); err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	if c.DryRun {
		if c.JSON {
			if err := report.WriteDryRunJSON(stdout, c, p); err != nil {
				fmt.Fprintf(stderr, "limitcheck: %s\n", err)
				return 2
			}
			return 0
		}
		report.WriteDryRun(stdout, c, p)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log, err := schedule.Execute(ctx, c, p)
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	verdicts := check.Run(c, log)
	if c.JSON {
		if err := report.WriteJSON(stdout, c, log, verdicts); err != nil {
			fmt.Fprintf(stderr, "limitcheck: %s\n", err)
			return 2
		}
	} else {
		report.WriteText(stdout, c, log, verdicts)
	}
	return report.ExitCode(verdicts)
}
