package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/perkinsjr/limitcheck/internal/brokenserver"
	"github.com/perkinsjr/limitcheck/internal/campaign"
	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/config"
	"github.com/perkinsjr/limitcheck/internal/render"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd, err := config.Parse(args)
	if errors.Is(err, config.ErrHelp) || (err == nil && cmd.Action == spec.ActionHelp) {
		fmt.Fprint(stdout, config.Usage())
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	switch cmd.Action {
	case spec.ActionSelfTest:
		return selftest(cmd, stdout, stderr)
	case spec.ActionDemo:
		return demo(cmd, stdout, stderr)
	case spec.ActionServe:
		return serve(cmd, stdout, stderr)
	default:
		return execute(context.Background(), cmd, stdout, stderr)
	}
}

func execute(ctx context.Context, cmd spec.Command, stdout, stderr io.Writer) int {
	checks, err := check.Selected(cmd.Input)
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	report, err := campaign.Run(ctx, cmd.Input, checks)
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	if err := writeReport(stdout, cmd.Input.JSON, report); err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	return report.ExitCode()
}

func writeReport(w io.Writer, asJSON bool, report render.Report) error {
	if asJSON {
		return render.WriteJSON(w, report)
	}
	render.WriteText(w, report)
	return nil
}

func serve(cmd spec.Command, stdout, stderr io.Writer) int {
	srv, err := brokenserver.Listen(cmd.Addr, cmd.Input.Policy.Rate, cmd.Algo)
	if err != nil {
		fmt.Fprintf(stderr, "limitcheck: %s\n", err)
		return 2
	}
	defer srv.Close()
	fmt.Fprintf(stdout, "serving %s %s\n", cmd.Algo, srv.URL())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	return 0
}
