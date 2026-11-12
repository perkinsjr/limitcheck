// Package check scores an observation log against the limit the user stated.
package check

import (
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Status is pass, warn, or fail.
type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Verdict is one check's result.
type Verdict struct {
	Kind    spec.CheckKind
	Status  Status
	Message string
	Metrics map[string]float64
}

// Run calls the selected checks in the order the user listed them.
func Run(c spec.Campaign, log observe.Log) []Verdict {
	out := make([]Verdict, 0, len(c.Checks))
	for _, kind := range c.Checks {
		fn, ok := analyzers[kind]
		if !ok {
			continue
		}
		out = append(out, fn(c, log))
	}
	return out
}

var analyzers = map[spec.CheckKind]func(spec.Campaign, observe.Log) Verdict{
	spec.CheckAccuracy:      accuracy,
	spec.CheckBoundary:      boundary,
	spec.CheckBurst:         burst,
	spec.CheckIsolation:     isolation,
	spec.CheckHeaders:       headersCheck,
	spec.CheckRecovery:      recovery,
	spec.CheckMultiInstance: multiInstance,
}

func verdict(kind spec.CheckKind, status Status, message string, metrics map[string]float64) Verdict {
	return Verdict{Kind: kind, Status: status, Message: message, Metrics: metrics}
}

func tolerance(n int) int {
	t := n / 10
	if t < 1 {
		return 1
	}
	return t
}

func countAllowed(events []observe.Event) int {
	n := 0
	for _, e := range events {
		if e.Err == "" && e.Allowed() {
			n++
		}
	}
	return n
}
