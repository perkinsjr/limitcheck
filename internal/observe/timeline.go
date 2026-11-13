// Package observe is the append-only record of what was sent.
package observe

import (
	"time"

	"github.com/perkinsjr/limitcheck/internal/hdr"
)

// Event is one attempt. Drift is SentAt minus GridAt and is not stored.
type Event struct {
	Check    string
	Wave     string
	Marker   string
	Origin   int
	Identity int
	GridAt   time.Time
	SentAt   time.Time
	// Edge is the window boundary when this request was part of a boundary burst.
	Edge    time.Time
	Status  int
	Reading hdr.Reading
	Err     string
	// Held means the slot was already past a MustHit gate, so it was not sent.
	Held bool
}

// Drift is how late the send left compared with the stamped grid time.
func (e Event) Drift() time.Duration {
	if e.SentAt.IsZero() || e.GridAt.IsZero() {
		return 0
	}
	return e.SentAt.Sub(e.GridAt)
}

// OK is a completed response in the 2xx or 3xx range.
func (e Event) OK() bool {
	return e.Err == "" && !e.Held && e.Status >= 200 && e.Status < 400
}

// Timeline is the log one check scores. Held names waves that were not sent.
type Timeline struct {
	Events []Event
	Held   []string
}
