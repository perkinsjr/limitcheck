// Package spec is the campaign vocabulary. Checks, the scheduler, and the
// CLI all speak these types. Nothing here dials or sleeps.
package spec

import (
	"net/url"
	"time"
)

const (
	DefaultMaxRequests = 200
	DefaultMaxInFlight = 64
	DefaultWall        = 30 * time.Minute
	DefaultPerRequest  = 10 * time.Second
	// MinWindow is the shortest limit a check can straddle without the
	// bursts landing on the same side of the edge.
	MinWindow = 400 * time.Millisecond
)

// Rate is the limit the operator claims the server enforces.
type Rate struct {
	Count  int
	Window time.Duration
}

// Policy is that claim. Checks score a timeline against it.
type Policy struct {
	Rate Rate
}

// Header is one request header.
type Header struct {
	Name  string
	Value string
}

// Identity is one caller. Two identities are how isolation tells clients apart.
type Identity struct {
	Name    string
	Headers []Header
}

// Origin is one place to send. Host is the literal host, never a lookup.
type Origin struct {
	Scheme   string
	Host     string
	Path     string
	RawQuery string
}

// URL is the request target for this origin.
func (o Origin) URL() string {
	return (&url.URL{Scheme: o.Scheme, Host: o.Host, Path: o.Path, RawQuery: o.RawQuery}).String()
}

// Caps bound a run. They are small on purpose.
type Caps struct {
	MaxRequests int
	MaxInFlight int
	Wall        time.Duration
	PerRequest  time.Duration
}

// DefaultCaps is the safety limit for a run that did not set its own.
func DefaultCaps() Caps {
	return Caps{
		MaxRequests: DefaultMaxRequests,
		MaxInFlight: DefaultMaxInFlight,
		Wall:        DefaultWall,
		PerRequest:  DefaultPerRequest,
	}
}

// Input is one invocation after flags and YAML are merged.
type Input struct {
	Method     string
	Body       string
	Headers    []Header
	Policy     Policy
	Identities []Identity
	Origins    []Origin
	Checks     []string
	KeyBy      []string
	Sharing    bool
	Caps       Caps
	Yes        bool
	DryRun     bool
	JSON       bool
}

// Kind says when a wave may fire.
type Kind int

const (
	// Immediate fires on the next tick.
	Immediate Kind = iota
	// AfterPrevious fires after the previous wave's last slot.
	AfterPrevious
	// AfterLast fires after the last request of the previous wave was sent.
	AfterLast
	// WhenIdle waits until the previous wave has finished.
	WhenIdle
	// SnapshotReset anchors the wave to a window boundary.
	SnapshotReset
	// RetryAfter uses times already taken from a sealed 429.
	RetryAfter
	// Ready waits out one full window so the next burst starts clean.
	Ready
)

// Gate is the closed set of ways a wave may wait.
type Gate struct {
	Kind Kind
	// MustHit means a slot that is already past its window is not sent.
	// A late straddle is not a boundary result.
	MustHit bool
}

// Action is what the process was asked to do.
type Action int

const (
	ActionRun Action = iota
	ActionHelp
	ActionDemo
	ActionSelfTest
	ActionServe
)

// Command is a parsed invocation.
type Command struct {
	Action   Action
	Input    Input
	Scenario string
	Addr     string
	Algo     string
}
