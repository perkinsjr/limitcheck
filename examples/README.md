# Examples

Runnable proofs that limitcheck catches (or correctly passes) every limiter mode the CLI checks.

## Quick prove-everything (local)

From the repo root:

```
./examples/run-all.sh
```

Builds `limitcheck`, runs `--self-test`, then each `demo --scenario`. Exit `0` only when every expected exit code matches.

| Command | Expected exit | What it proves |
|---------|---------------|----------------|
| `limitcheck --self-test` | `0` | Every built-in scenario matches its claim |
| `demo --scenario token` | `0` | Good sliding window: headers, accuracy, boundary, burst, recovery, isolation **pass** |
| `demo --scenario fixed` | `1` | Fixed-window **boundary** leak (~2× across the edge) |
| `demo --scenario race` | `1` | Concurrent **burst** race |
| `demo --scenario open` | `1` | **accuracy** — steady run allows more than the stated limit |
| `demo --scenario bad-headers` | `1` | **headers** — rejection is not a proper 429 shape |
| `demo --scenario early` | `1` | **recovery** — success before the advertised reset |
| `demo --scenario rotate` | `1` | **isolation** — rotating `X-Limitcheck-Request` mints a new bucket |
| `demo --scenario per-instance` | `1` | **multi_instance** — each origin keeps its own full limit under `--sharing` |
| `demo --scenario shared` | `1` | Shared counter: multi_instance **pass**, isolation **fail** |

`fixed-window` / `racy` / `correct` are older aliases for `fixed` / `race` / `token`.

## Unkey (real remote limiter)

`examples/unkey` is a tiny local HTTP server that enforces the limit via Unkey's [`ratelimit.limit`](https://www.unkey.com/docs/api-management/api-reference/ratelimit/apply-rate-limiting) API, then returns the 429 / header / JSON-reset shape limitcheck scores.

```
export UNKEY_ROOT_KEY=unkey_...   # root key with ratelimit.*.limit — never commit this
# optional:
# export UNKEY_NAMESPACE=limitcheck.examples
# export UNKEY_LIMIT=5
# export UNKEY_WINDOW_MS=2000
./examples/unkey/run.sh
```

| Env | Required | Default | Meaning |
|-----|----------|---------|---------|
| `UNKEY_ROOT_KEY` | yes | — | Unkey root key (`ratelimit.*.limit`) |
| `UNKEY_NAMESPACE` | no | `limitcheck.examples` | Unkey ratelimit namespace |
| `UNKEY_LIMIT` | no | `5` | Requests per window |
| `UNKEY_WINDOW_MS` | no | `2000` | Window in milliseconds (≥ 400) |

Expected: exit `0` — headers, accuracy, boundary, burst, recovery, and isolation all pass against the Unkey-backed target.

If `UNKEY_ROOT_KEY` is missing, the script exits `2` and prints what to set. It does not invent credentials.
