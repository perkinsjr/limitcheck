// Package spec parses a campaign at the CLI and config boundary.
package spec

import (
	"net/url"
	"time"
)

// CheckKind names one analyzer.
type CheckKind string

const (
	CheckAccuracy      CheckKind = "accuracy"
	CheckBoundary      CheckKind = "boundary"
	CheckBurst         CheckKind = "burst"
	CheckIsolation     CheckKind = "isolation"
	CheckHeaders       CheckKind = "headers"
	CheckRecovery      CheckKind = "recovery"
	CheckMultiInstance CheckKind = "multi_instance"
)

// DefaultChecks are the analyzers a run uses when the user does not pick any.
var DefaultChecks = []CheckKind{
	CheckAccuracy,
	CheckBoundary,
	CheckBurst,
	CheckIsolation,
	CheckHeaders,
	CheckRecovery,
}

// Tag is why a slot exists. Checks read the tag off each recorded request.
type Tag string

const (
	TagSteady       Tag = "steady"
	TagPreBoundary  Tag = "pre_boundary"
	TagPostBoundary Tag = "post_boundary"
	TagBurst        Tag = "burst"
	TagProbe        Tag = "probe"
)

// Action is the command the user invoked.
type Action string

const (
	ActionRun      Action = "run"
	ActionDemo     Action = "demo"
	ActionSelfTest Action = "self-test"
)

// Limit is a count allowed inside one window.
type Limit struct {
	Count  int
	Window time.Duration
}

// Header is one request header. Name keeps the user's spelling.
type Header struct {
	Name  string
	Value string
}

// Identity is the header set that should be one client for the limiter.
type Identity struct {
	Name    string
	Headers []Header
}

// Target is the request that every slot sends, before origin and identity.
type Target struct {
	URL     *url.URL
	Method  string
	Body    string
	Headers []Header
}

// Origin is the scheme and host a slot is sent to.
type Origin struct {
	Scheme string
	Host   string
}

// Campaign is a parsed run. Later packages trust it.
type Campaign struct {
	Target      Target
	Limit       Limit
	Identities  []Identity
	Origins     []Origin
	Checks      []CheckKind
	MaxRequests int
	Yes         bool
	DryRun      bool
	JSON        bool
}

// Command is the parsed invocation.
type Command struct {
	Action   Action
	Scenario string
	Campaign Campaign
}

// DefaultMaxRequests is the cap when the user does not set one.
const DefaultMaxRequests = 200
