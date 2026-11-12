// Package observe records what each planned request actually did.
package observe

import (
	"net/http"
	"time"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Event is one finished request.
type Event struct {
	SlotID   int
	Tag      spec.Tag
	Origin   int
	Identity int
	FireAt   time.Time
	Started  time.Time
	Ended    time.Time
	Status   int
	Header   http.Header
	Body     string
	Err      string
}

// Allowed reports whether the server accepted the request.
func (e Event) Allowed() bool {
	return e.Err == "" && e.Status >= 200 && e.Status < 400
}

// Drift is how late the request started compared with its planned fire time.
func (e Event) Drift() time.Duration {
	if e.Started.IsZero() {
		return 0
	}
	return e.Started.Sub(e.FireAt)
}

// Log is the campaign record the checks read.
type Log struct {
	Events     []Event
	BoundaryAt time.Time
	Limit      spec.Limit
}

// MaxDrift is the largest late start in the log.
func (l Log) MaxDrift() time.Duration {
	var max time.Duration
	for _, e := range l.Events {
		if d := e.Drift(); d > max {
			max = d
		}
	}
	return max
}
