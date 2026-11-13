package render

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

type WaveLine struct {
	Check string
	Name  string
	Count int
}

type Report struct {
	URL      string
	Limit    spec.Rate
	Verdicts []check.Verdict
	Events   []observe.Event
	DryRun   bool
	Planned  int
	Waves    []WaveLine
}

// ExitCode maps the report to a process status.
// Fail is 1. Inconclusive with no fail is 2. Pass, warn, and dry-run are 0.
func (r Report) ExitCode() int {
	if r.DryRun {
		return 0
	}
	fail, inc := 0, 0
	for _, v := range r.Verdicts {
		switch v.Status {
		case check.Fail:
			fail++
		case check.Inconclusive:
			inc++
		}
	}
	if fail > 0 {
		return 1
	}
	if inc > 0 {
		return 2
	}
	return 0
}

func WriteText(w io.Writer, r Report) {
	if r.DryRun {
		fmt.Fprintf(w, "dry run, %d requests, nothing sent\n", r.Planned)
		fmt.Fprintf(w, "limit %d per %s\n", r.Limit.Count, r.Limit.Window)
		for _, wave := range r.Waves {
			fmt.Fprintf(w, "%-14s %-10s %d\n", wave.Check, wave.Name, wave.Count)
		}
		return
	}
	fmt.Fprintf(w, "target %s\n", r.URL)
	fmt.Fprintf(w, "limit %d per %s\n", r.Limit.Count, r.Limit.Window)
	sent, allowed, rejected, errs, drift := tally(r.Events)
	fmt.Fprintf(w, "sent %d, allowed %d, rejected %d, errors %d, max drift %s\n\n",
		sent, allowed, rejected, errs, drift.Round(time.Millisecond))
	for _, v := range r.Verdicts {
		fmt.Fprintf(w, "%-14s %-12s  %s\n", v.Name, v.Status, v.Message)
	}
	fmt.Fprintln(w)
	if line, span := Timeline(r.Events, 60); line != "" {
		fmt.Fprintf(w, "timeline %s\n%s\n", span, line)
		fmt.Fprintln(w, "+ allowed   x rejected   # both   . idle")
		fmt.Fprintln(w)
	}
	switch n := failed(r.Verdicts); {
	case n == 0 && inconclusive(r.Verdicts) > 0:
		fmt.Fprintln(w, "no check failed, some were inconclusive")
	case n == 0:
		fmt.Fprintln(w, "no check failed")
	case n == 1:
		fmt.Fprintln(w, "1 check failed")
	default:
		fmt.Fprintf(w, "%d checks failed\n", n)
	}
}

func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if r.DryRun {
		return enc.Encode(struct {
			DryRun   bool       `json:"dry_run"`
			Target   string     `json:"target"`
			Limit    any        `json:"limit"`
			Requests int        `json:"requests"`
			Waves    []WaveLine `json:"waves"`
		}{
			DryRun: true, Target: r.URL, Limit: limitJSON(r.Limit), Requests: r.Planned, Waves: r.Waves,
		})
	}
	sent, allowed, rejected, errs, drift := tally(r.Events)
	line, _ := Timeline(r.Events, 60)
	return enc.Encode(struct {
		OK           bool            `json:"ok"`
		Target       string          `json:"target"`
		Limit        any             `json:"limit"`
		Sent         int             `json:"sent"`
		Allowed      int             `json:"allowed"`
		Rejected     int             `json:"rejected"`
		Errors       int             `json:"errors"`
		MaxDriftMS   int64           `json:"max_drift_ms"`
		Failed       int             `json:"failed"`
		Inconclusive int             `json:"inconclusive"`
		Verdicts     []check.Verdict `json:"verdicts"`
		Timeline     string          `json:"timeline"`
	}{
		OK:           failed(r.Verdicts) == 0,
		Target:       r.URL,
		Limit:        limitJSON(r.Limit),
		Sent:         sent,
		Allowed:      allowed,
		Rejected:     rejected,
		Errors:       errs,
		MaxDriftMS:   drift.Milliseconds(),
		Failed:       failed(r.Verdicts),
		Inconclusive: inconclusive(r.Verdicts),
		Verdicts:     r.Verdicts,
		Timeline:     line,
	})
}

func limitJSON(r spec.Rate) any {
	return struct {
		Count  int    `json:"count"`
		Window string `json:"window"`
	}{Count: r.Count, Window: r.Window.String()}
}

func failed(vs []check.Verdict) int {
	n := 0
	for _, v := range vs {
		if v.Status == check.Fail {
			n++
		}
	}
	return n
}

func inconclusive(vs []check.Verdict) int {
	n := 0
	for _, v := range vs {
		if v.Status == check.Inconclusive {
			n++
		}
	}
	return n
}

func tally(events []observe.Event) (sent, allowed, rejected, errs int, drift time.Duration) {
	for _, e := range events {
		if e.Held {
			continue
		}
		sent++
		switch {
		case e.Err != "":
			errs++
		case e.OK():
			allowed++
		default:
			rejected++
		}
		if d := e.Drift(); d > drift {
			drift = d
		}
	}
	return
}

func Timeline(events []observe.Event, width int) (string, string) {
	if width < 4 {
		width = 4
	}
	var start, end time.Time
	for _, e := range events {
		if e.Held || e.SentAt.IsZero() {
			continue
		}
		if start.IsZero() || e.SentAt.Before(start) {
			start = e.SentAt
		}
		if end.IsZero() || e.SentAt.After(end) {
			end = e.SentAt
		}
	}
	if start.IsZero() {
		return "", ""
	}
	if !end.After(start) {
		end = start.Add(time.Millisecond)
	}
	span := end.Sub(start)
	type cell struct{ allow, deny bool }
	cells := make([]cell, width)
	for _, e := range events {
		if e.Held || e.SentAt.IsZero() || e.Err != "" {
			continue
		}
		pos := int(e.SentAt.Sub(start) * time.Duration(width) / span)
		if pos >= width {
			pos = width - 1
		}
		if pos < 0 {
			pos = 0
		}
		if e.OK() {
			cells[pos].allow = true
		} else {
			cells[pos].deny = true
		}
	}
	buf := make([]byte, width)
	for i, c := range cells {
		switch {
		case c.allow && c.deny:
			buf[i] = '#'
		case c.allow:
			buf[i] = '+'
		case c.deny:
			buf[i] = 'x'
		default:
			buf[i] = '.'
		}
	}
	return string(buf), span.Round(time.Millisecond).String()
}
