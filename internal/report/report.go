// Package report prints a campaign result as text or JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/observe"
	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// ExitCode is 1 when any check failed.
func ExitCode(vs []check.Verdict) int {
	if Failed(vs) > 0 {
		return 1
	}
	return 0
}

// Failed counts fail verdicts.
func Failed(vs []check.Verdict) int {
	n := 0
	for _, v := range vs {
		if v.Status == check.Fail {
			n++
		}
	}
	return n
}

// WriteText prints the human report.
func WriteText(w io.Writer, c spec.Campaign, log observe.Log, vs []check.Verdict) {
	fmt.Fprintf(w, "target %s\n", target(c))
	fmt.Fprintf(w, "limit %d per %s\n", c.Limit.Count, c.Limit.Window)
	sent, allowed, rejected, errs := tally(log)
	fmt.Fprintf(w, "sent %d, allowed %d, rejected %d, errors %d, max drift %s\n\n",
		sent, allowed, rejected, errs, log.MaxDrift().Round(time.Millisecond))
	for _, v := range vs {
		fmt.Fprintf(w, "%-14s %-4s  %s\n", v.Kind, v.Status, v.Message)
	}
	fmt.Fprintln(w)
	if line, span := Timeline(log.Events, 60); line != "" {
		fmt.Fprintf(w, "timeline %s\n%s\n", span, line)
		fmt.Fprintln(w, "+ allowed   x rejected   # both   . idle")
		fmt.Fprintln(w)
	}
	switch n := Failed(vs); n {
	case 0:
		fmt.Fprintln(w, "no check failed")
	case 1:
		fmt.Fprintln(w, "1 check failed")
	default:
		fmt.Fprintf(w, "%d checks failed\n", n)
	}
}

// WriteDryRun prints the plan and states that nothing was sent.
func WriteDryRun(w io.Writer, c spec.Campaign, p plan.Plan) {
	fmt.Fprintf(w, "dry run, %d requests, nothing sent\n", len(p.Slots))
	fmt.Fprintf(w, "limit %d per %s\n", c.Limit.Count, c.Limit.Window)
	if !p.BoundaryAt.IsZero() {
		fmt.Fprintf(w, "window edge %s\n", p.BoundaryAt.Format(time.RFC3339Nano))
	}
	for _, slot := range p.Slots {
		name := "default"
		if slot.Identity >= 0 && slot.Identity < len(c.Identities) {
			name = c.Identities[slot.Identity].Name
		}
		fmt.Fprintf(w, "%s  %s  %s  origin %d\n", slot.FireAt.Format(time.RFC3339Nano), slot.Tag, name, slot.Origin)
	}
}

// WriteJSON prints the machine report.
func WriteJSON(w io.Writer, c spec.Campaign, log observe.Log, vs []check.Verdict) error {
	payload := buildJSON(c, log, vs)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// WriteDryRunJSON prints the plan as JSON.
func WriteDryRunJSON(w io.Writer, c spec.Campaign, p plan.Plan) error {
	type slot struct {
		ID       int       `json:"id"`
		FireAt   time.Time `json:"fire_at"`
		Origin   int       `json:"origin"`
		Identity string    `json:"identity"`
		Tag      string    `json:"tag"`
	}
	body := struct {
		DryRun   bool   `json:"dry_run"`
		Target   string `json:"target"`
		Limit    any    `json:"limit"`
		Requests int    `json:"requests"`
		Slots    []slot `json:"slots"`
	}{
		DryRun:   true,
		Target:   target(c),
		Limit:    limitJSON(c),
		Requests: len(p.Slots),
	}
	for _, s := range p.Slots {
		name := "default"
		if s.Identity >= 0 && s.Identity < len(c.Identities) {
			name = c.Identities[s.Identity].Name
		}
		body.Slots = append(body.Slots, slot{
			ID: s.ID, FireAt: s.FireAt, Origin: s.Origin, Identity: name, Tag: string(s.Tag),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(body)
}

// Timeline draws allowed and rejected requests across the run.
// The second return value is the span label.
func Timeline(events []observe.Event, width int) (string, string) {
	if width < 4 {
		width = 4
	}
	var start, end time.Time
	for _, e := range events {
		if e.Started.IsZero() {
			continue
		}
		if start.IsZero() || e.Started.Before(start) {
			start = e.Started
		}
		stop := e.Ended
		if stop.IsZero() {
			stop = e.Started
		}
		if end.IsZero() || stop.After(end) {
			end = stop
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
		if e.Started.IsZero() || e.Err != "" {
			continue
		}
		pos := int(e.Started.Sub(start) * time.Duration(width) / span)
		if pos >= width {
			pos = width - 1
		}
		if pos < 0 {
			pos = 0
		}
		if e.Allowed() {
			cells[pos].allow = true
		} else if e.Status > 0 {
			cells[pos].deny = true
		}
	}
	line := make([]byte, width)
	for i, c := range cells {
		switch {
		case c.allow && c.deny:
			line[i] = '#'
		case c.allow:
			line[i] = '+'
		case c.deny:
			line[i] = 'x'
		default:
			line[i] = '.'
		}
	}
	label := fmt.Sprintf("%s to %s", start.Format("15:04:05.000"), end.Format("15:04:05.000"))
	return string(line), label
}

func tally(log observe.Log) (sent, allowed, rejected, errs int) {
	for _, e := range log.Events {
		sent++
		switch {
		case e.Err != "":
			errs++
		case e.Allowed():
			allowed++
		default:
			rejected++
		}
	}
	return sent, allowed, rejected, errs
}

func target(c spec.Campaign) string {
	if c.Target.URL == nil {
		return ""
	}
	return c.Target.URL.String()
}

type verdictJSON struct {
	Kind    string             `json:"kind"`
	Status  string             `json:"status"`
	Message string             `json:"message"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
}

func buildJSON(c spec.Campaign, log observe.Log, vs []check.Verdict) map[string]any {
	sent, allowed, rejected, errs := tally(log)
	line, span := Timeline(log.Events, 60)
	out := make([]verdictJSON, len(vs))
	for i, v := range vs {
		out[i] = verdictJSON{Kind: string(v.Kind), Status: string(v.Status), Message: v.Message, Metrics: v.Metrics}
	}
	return map[string]any{
		"target":        target(c),
		"limit":         limitJSON(c),
		"sent":          sent,
		"allowed":       allowed,
		"rejected":      rejected,
		"errors":        errs,
		"max_drift_ms":  float64(log.MaxDrift().Microseconds()) / 1000,
		"verdicts":      out,
		"timeline":      line,
		"timeline_span": span,
		"failed":        Failed(vs),
		"ok":            Failed(vs) == 0,
	}
}

func limitJSON(c spec.Campaign) map[string]any {
	return map[string]any{
		"count":  c.Limit.Count,
		"window": c.Limit.Window.String(),
	}
}
