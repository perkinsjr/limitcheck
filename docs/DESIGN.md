# limitcheck design

## Problem

Teams rarely test rate limits until something breaks. limitcheck is a Go CLI that states an intended limit, sends a planned traffic pattern, and reports pass, warn, or fail for each check.

## Usage

```
limitcheck https://api.example.com/v1/items \
  --limit 100/60s \
  --header "Authorization: Bearer $KEY"
```

```
limitcheck --config limitcheck.yaml --json
```

```
limitcheck demo --scenario fixed-window   # built-in broken server + self-check
```

## Shape

Base is a sealed Campaign plan, then execute, then analyze.

1. Parse CLI or YAML into `CampaignSpec` at the boundary.
2. `Plan(spec)` builds a `Plan` of request slots with absolute fire times.
3. `Safety.Check(plan)` enforces localhost or `--yes`, and `--max-requests`.
4. `--dry-run` prints the plan and exits 0.
5. `Scheduler` fires slots while tracking clock drift.
6. Each response becomes a `RequestEvent` in an `ObservationLog` / `Timeline`.
7. Registered checks analyze the log and return `Verdict` values.
8. `Reporter` prints terminal or JSON and sets the process exit code.

### Core types

- `Limit` — count + window duration (parsed from `100/60s`)
- `Identity` — header set that defines one client
- `Target` — URL, method, body, base headers
- `CampaignSpec` — targets, limit, identities, check kinds, origins, safety flags
- `Slot` — fire-at time, target index, identity index, tag (steady|pre_boundary|post_boundary|burst|probe)
- `Plan` — ordered slots, expected totals, window anchors
- `RequestEvent` — slot id, start/end, status, headers, body snippet, error
- `ObservationLog` — events + timeline buckets
- `Check` — `Name() CheckKind`; `Analyze(log, spec) Verdict`
- `Verdict` — status (pass|warn|fail), message, metrics map

### Packages

- `cmd/limitcheck` — thin CLI
- `internal/spec` — parse limit strings, flags, YAML
- `internal/plan` — build Plan from CampaignSpec
- `internal/safety` — pure gates
- `internal/schedule` — drift-aware executor
- `internal/observe` — RequestEvent, ObservationLog
- `internal/check` — registry + analyzers
- `internal/report` — text, JSON, ASCII timeline
- `internal/testserver` — broken limiters for demos and tests

### Checks

| Kind | Claim |
|------|--------|
| accuracy | allowed count near limit over a steady window |
| boundary | no ~2x leak across fixed-window edges |
| burst | concurrent N does not exceed limit by race |
| isolation | identity A exhaustion does not throttle B; header rotation does not bypass |
| multi_instance | optional; shared vs per-origin counting |
| recovery | after 429, success resumes at Retry-After/reset, not earlier |
| headers | 429 status, Retry-After, RateLimit / X-RateLimit consistency |

### Safety defaults

- Default max requests: 200
- Non-loopback hosts require `--yes`
- `--dry-run` never sends

## Tradeoffs

- We accept a two-phase plan/execute model in exchange for dry-run and measurable timing.
- We accept ASCII timeline charts first, not a TUI, to keep the binary simple.
- Multi-instance is opt-in via multiple `--origin` values.

## Next step

Implement module scaffold and a demo that fails `boundary` against a fixed-window test server.
