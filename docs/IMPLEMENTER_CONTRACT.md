# Implementer contract (supersedes earlier sealed-CampaignPlan brief)

Pull `main` before your next commit. `docs/DESIGN.md` is the contract.

**Base shape.** Check registry with pure `Verdict(policy, timeline)`. Each check's `Stimulus` returns immutable waves. Plans are ephemeral per wave, not the extension point.

**Must follow candidate-1 contracts.**
- Packages: `spec`, `config`, `check`, `sched`, `exec`, `observe`, `hdr`, `campaign`, `render`, `brokenserver`
- `hdr.Interpret` / `hdr.Write` only for rate-limit wire bytes
- Closed `Gate` algebra; no sleep-in-a-loop; no grid rebase
- Exit codes: fail 1, inconclusive 2, pass/warn/dry-run 0
- `limitcheck serve --algo token|fixed|race`
- Registry `All` + AST test that every Verdict type is listed
- Push verified work to `main` only. No PR.

If you already scaffolded a global CampaignPlan as the extension point, scrap it and rebuild against this contract rather than bolting checks onto it.

**Grafts from arena candidate-2 (keep these behaviors).**
- Stamp `GridAt = epoch + offset` right before a wave fires.
- Admit with literal loopback host check only (no DNS in the safety gate).
- Recovery arms at most one follower wave from Retry-After after the exhaust log seals.
- Never retry HTTP; record transport errors as events.
