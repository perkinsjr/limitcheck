package check

import (
	"fmt"
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/sched"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Check is one named measurement. Stimulus builds the waves. Verdict scores the log.
type Check struct {
	Name             string
	IncludeByDefault func(spec.Input) bool
	Stimulus         func(spec.Input) sched.Stimulus
	Verdict          func(spec.Policy, observe.Timeline) Verdict
	// Follow may arm one wave from a sealed log. Recovery is the only caller.
	Follow func(observe.Timeline, time.Time) (sched.Wave, bool)
}

// All is the registry, in report order.
func All() []Check {
	return []Check{
		HeadersCheck(),
		AccuracyCheck(),
		BoundaryCheck(),
		BurstCheck(),
		RecoveryCheck(),
		IsolationCheck(),
		MultiInstanceCheck(),
	}
}

// Selected returns the checks for this run.
// An empty list uses each check's own default rule.
func Selected(in spec.Input) ([]Check, error) {
	all := All()
	if len(in.Checks) == 0 {
		var out []Check
		for _, c := range all {
			if c.IncludeByDefault != nil && c.IncludeByDefault(in) {
				out = append(out, c)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no checks selected")
		}
		return out, nil
	}
	by := map[string]Check{}
	for _, c := range all {
		by[c.Name] = c
	}
	by["multi-instance"] = by["multi_instance"]
	var out []Check
	seen := map[string]bool{}
	for _, name := range in.Checks {
		c, ok := by[name]
		if !ok {
			return nil, fmt.Errorf("unknown check %q", name)
		}
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out, nil
}
