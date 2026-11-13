package campaign

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/exec"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/render"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Run executes the checks. A dry run returns the wave table and dials nothing.
// A cancelled run returns an error and no report, so a crash does not print a partial result.
func Run(ctx context.Context, in spec.Input, checks []check.Check) (render.Report, error) {
	if len(checks) == 0 {
		return render.Report{}, fmt.Errorf("no checks selected")
	}
	if len(in.Origins) == 0 {
		return render.Report{}, fmt.Errorf("no origin")
	}
	if in.Policy.Rate.Count < 1 {
		return render.Report{}, fmt.Errorf("limit count must be at least 1")
	}
	if in.Policy.Rate.Window < spec.MinWindow {
		return render.Report{}, fmt.Errorf("window %s is shorter than %s, so the edge bursts would land together", in.Policy.Rate.Window, spec.MinWindow)
	}
	if in.Caps.MaxRequests == 0 {
		in.Caps = spec.DefaultCaps()
	}
	if in.Caps.Wall == 0 {
		in.Caps.Wall = spec.DefaultWall
	}
	if in.Caps.PerRequest == 0 {
		in.Caps.PerRequest = spec.DefaultPerRequest
	}
	if in.Caps.MaxInFlight == 0 {
		in.Caps.MaxInFlight = spec.DefaultMaxInFlight
	}

	stimuli := make([]sched.Stimulus, len(checks))
	var lines []render.WaveLine
	cost := 0
	for i, ch := range checks {
		stimuli[i] = ch.Stimulus(in)
		for _, w := range stimuli[i].Waves {
			n := sched.Cost([]sched.Wave{w}, len(in.Origins))
			cost += n
			lines = append(lines, render.WaveLine{Check: ch.Name, Name: w.Name, Count: n})
		}
		if ch.Follow != nil {
			cost += 2
			lines = append(lines, render.WaveLine{Check: ch.Name, Name: "follower", Count: 2})
		}
	}
	if err := sched.WithinCaps(cost, in.Caps); err != nil {
		return render.Report{}, err
	}
	report := render.Report{
		URL: in.Origins[0].URL(), Limit: in.Policy.Rate,
		DryRun: in.DryRun, Planned: cost, Waves: lines,
	}
	if in.DryRun {
		return report, nil
	}
	if err := sched.Admit(in); err != nil {
		return render.Report{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, in.Caps.Wall)
	defer cancel()
	client := exec.Client(in.Caps.PerRequest)
	if tr, ok := client.Transport.(*http.Transport); ok {
		defer tr.CloseIdleConnections()
	}

	var events []observe.Event
	for i, ch := range checks {
		tl, err := dispatch(ctx, in, client, stimuli[i].Waves, ch.Name)
		if err != nil {
			return render.Report{}, err
		}
		if ch.Follow != nil {
			if wave, ok := ch.Follow(tl, time.Now()); ok {
				more, err := dispatch(ctx, in, client, []sched.Wave{wave}, ch.Name)
				if err != nil {
					return render.Report{}, err
				}
				tl.Events = append(tl.Events, more.Events...)
				tl.Held = append(tl.Held, more.Held...)
			}
		}
		report.Verdicts = append(report.Verdicts, ch.Verdict(in.Policy, tl))
		events = append(events, tl.Events...)
	}
	report.Events = events
	return report, nil
}

func dispatch(ctx context.Context, in spec.Input, client *http.Client, waves []sched.Wave, checkName string) (observe.Timeline, error) {
	var tl observe.Timeline
	origins := len(in.Origins)
	window := in.Policy.Rate.Window
	for i := 0; i < len(waves); {
		var planned []sched.Planned
		if waves[i].Anchor != "" && waves[i].Gate.Kind == spec.SnapshotReset {
			j := i + 1
			for j < len(waves) && waves[j].Anchor == waves[i].Anchor {
				j++
			}
			planned = sched.StampEdge(waves[i:j], time.Now(), window, origins)
			i = j
		} else {
			planned = []sched.Planned{sched.Stamp(waves[i], time.Now(), window, origins)}
			i++
		}
		for _, p := range planned {
			if p.Held {
				tl.Held = append(tl.Held, p.Wave.Name)
				continue
			}
			ev, err := exec.Send(ctx, in, client, p, checkName)
			if err != nil {
				return observe.Timeline{}, err
			}
			for _, e := range ev {
				if e.Held {
					tl.Held = append(tl.Held, e.Wave)
					continue
				}
				tl.Events = append(tl.Events, e)
			}
		}
	}
	return tl, nil
}
