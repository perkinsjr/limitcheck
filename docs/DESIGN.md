# limitcheck design

## Problem

Teams rarely test rate limits until something breaks. limitcheck is a Go CLI that states an intended limit, sends the traffic that would expose a specific limiter bug, and reports pass, warn, fail, or inconclusive for each check.

## Usage

```
limitcheck https://api.example.com/v1/items \
  --limit 100/60s \
  --header "Authorization: Bearer $KEY" \
  --yes
```

Default checks run in registry order: headers, accuracy, boundary, burst, recovery. Isolation joins when two identities or `--key-by` are present. Multi-instance joins when two `--origin` values and `--sharing` are set.

```
limitcheck --config limitcheck.yaml --json
limitcheck --dry-run --limit 100/60s https://api.example.com/v1/items
limitcheck serve --addr 127.0.0.1:0 --limit 10/2s --algo fixed
```

Exit codes: any fail → 1; no fail but inconclusive → 2; pass/warn and dry-run → 0; did not start → 2.

## Shape

Base is a check registry with pure verdicts. Each check owns its stimulus and its scoring.

1. Parse CLI or YAML into `spec.Input` at the boundary.
2. Select checks from registry `All` (or named `--check` values).
3. For each check, `Stimulus` returns immutable waves (counts, in-flight, interval, identities, fanout, `Gate`).
4. Preflight `sched.Cost` against `--max-requests`. Refuse hosts whose URL host is not literal loopback (`127.0.0.1`, `::1`, `localhost`) without `--yes`. No DNS inside the gate. Dry-run skips that guard.
5. `--dry-run` prints the wave table and exits 0 without dialing.
6. `sched` expands each wave to clock-free offsets, then stamps `GridAt = epoch + offset` immediately before that wave fires. One timer per wait. No rebase after drift. Missed `MustHit` gates hold the wave. Recovery may arm one follower wave from the sealed exhaust log's Retry-After; that follower cannot defer again.
7. `exec` sends at most once per slot and appends `RequestEvent` values (`GridAt`, `SentAt`, status, reading). Drift is `SentAt - GridAt`, derived not stored.
8. `hdr.Interpret` is the only reader of status and rate-limit headers. `hdr.Write` is the only writer, including the broken server.
9. `Verdict(policy, timeline)` is pure. No context, clock, or client.
10. `render` prints text or JSON. `Report.ExitCode` is the only status-to-process map.

### Core types

- `Rate` / `Policy` — count + window from `N/<duration>`
- `Identity`, `Template`, `Origin`, `Input`, `Caps`
- `Gate` — closed algebra: Immediate, AfterPrevious, AfterLast, WhenIdle, SnapshotReset, RetryAfter, Ready
- `Wave`, `Stimulus`, `Phase`
- `Reading`, `RequestEvent`, `Marker`, `Timeline`
- `Check` — Name, IncludeByDefault, Stimulus, Verdict
- `Verdict`, `Report`, `Status` (pass|warn|fail|inconclusive)

### Packages

- `cmd/limitcheck` — thin CLI (`run`, `serve`)
- `internal/spec` — parse rate, caps, Input
- `internal/config` — flags + YAML → Input
- `internal/check` — registry `All` and each check file
- `internal/sched` — cost, plan wave, wait
- `internal/exec` — send, append timeline
- `internal/observe` — Timeline
- `internal/hdr` — Interpret / Write
- `internal/campaign` — Run loop
- `internal/render` — text, JSON, sparkline
- `internal/brokenserver` — token, fixed, race algos

### Checks

| Kind | Claim |
|------|--------|
| headers | 429 (not 200/403/503), Retry-After, RateLimit or X-RateLimit consistency |
| accuracy | steady pace stays near the stated quota |
| boundary | no ~2× leak across a fixed-window reset |
| burst | overlapping requests do not exceed the quota by race |
| isolation | one identity's exhaustion does not throttle another; header rotation does not bypass |
| multi-instance | optional; shared vs per-origin counting |
| recovery | after 429, success resumes at Retry-After/reset, not earlier |

### Safety

- Defaults: 200 requests, 64 in flight, 30 minutes wall, 10s per request
- Non-literal-loopback hosts require `--yes`. Dry-run skips that guard.
- No HTTP retries. Transport errors are observations. No partial stdout report on crash.

### Proof

Each check has a unit verdict on a hand-built timeline and a proof through `campaign.Run` against `brokenserver`. Unkey is mentioned in the README only as one atomic, identity-keyed fix path when a check fails.

## Tradeoffs

- We accept quiet waits up to a policy window so measurements start on a restored quota.
- We accept inconclusive when a MustHit placement is already past, instead of sending a late straddle and calling it a boundary result.
- We accept Check as the extension point, not a global sealed campaign script.

## Synthesis

Base: candidate-1 (check registry, pure Verdict, closed Gate, hdr Interpret/Write).

Grafts from candidate-2: stamp `epoch+offset` immediately before fire; pure admit on literal loopback (no DNS); recovery as one follower reserve armed from the sealed exhaust log; no open phase loop; transport errors are observations, never retried.

Rejected from candidate-2 as the base: global sealed CampaignSpec/claims as the extension point; single flat package of check rows; putting every absolute time in one phase-0 plan (loses Retry-After-driven recovery).

## Next step

Implement observe + hdr roundtrip, then boundary.Verdict against hand-built timelines, then brokenserver fixed-window proof.
