package check

import (
	"time"

	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

type Status string

const (
	Pass         Status = "pass"
	Warn         Status = "warn"
	Fail         Status = "fail"
	Inconclusive Status = "inconclusive"
)

type Verdict struct {
	Name    string
	Status  Status
	Message string
	Metrics map[string]float64
}

func v(name string, status Status, message string, metrics map[string]float64) Verdict {
	return Verdict{Name: name, Status: status, Message: message, Metrics: metrics}
}

func tolerance(n int) int {
	t := n / 10
	if t < 1 {
		return 1
	}
	return t
}

func marked(tl observe.Timeline, marker string) []observe.Event {
	var out []observe.Event
	for _, e := range tl.Events {
		if e.Marker == marker && !e.Held {
			out = append(out, e)
		}
	}
	return out
}

func held(tl observe.Timeline, name string) bool {
	for _, h := range tl.Held {
		if h == name {
			return true
		}
	}
	return false
}

func countAllowed(events []observe.Event) int {
	n := 0
	for _, e := range events {
		if e.OK() {
			n++
		}
	}
	return n
}

func always(spec.Input) bool { return true }

func extra(n int) int {
	e := n / 5
	if e < 2 {
		return 2
	}
	return e
}

func steadyGap(n int, window time.Duration) time.Duration {
	count := n + extra(n)
	gap := 30 * time.Millisecond
	if count > 1 {
		budget := window / 3
		if budget > 0 && time.Duration(count-1)*gap > budget {
			gap = budget / time.Duration(count-1)
		}
		if gap < time.Millisecond {
			gap = time.Millisecond
		}
	}
	return gap
}
