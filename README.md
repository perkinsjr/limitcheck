# limitcheck

You name the rate limit you expect. limitcheck sends the traffic that would expose a bad limiter, then prints pass, warn, fail, or inconclusive for each check.

Exit codes: any fail → `1`; no fail but something inconclusive → `2`; pass, warn, or dry-run → `0`; did not start → `2`.

## Build

```
go build -o limitcheck ./cmd/limitcheck
```

Needs Go 1.22+.

## Fastest path: prove the tool works

```
./limitcheck --self-test
```

```
token          ok  headers pass; accuracy pass; boundary pass; burst pass; recovery pass; isolation pass
fixed          ok  headers pass; accuracy pass; boundary fail; burst pass; recovery pass
race           ok  burst fail
per-instance   ok  multi_instance fail
shared         ok  multi_instance pass; isolation fail
early          ok  recovery fail
bad-headers    ok  headers fail
open           ok  accuracy fail
rotate         ok  isolation fail
self-test passed
```

Exit `0` means every broken local limiter failed the check that matches its bug, and the good limiter (`token`) passed.

## Check one broken limiter and read the report

```
./limitcheck demo --scenario fixed
```

```
scenario fixed
target http://127.0.0.1:43573/
limit 5 per 500ms
sent 40, allowed 31, rejected 9, errors 0, max drift 1ms

headers        pass          429 responses include Retry-After, a rate-limit header, and a JSON reset, and remaining does not go up
accuracy       pass          steady run allowed 5 requests, limit is 5
boundary       fail          allowed 10 requests across the window edge, limit is 5 (2.0x)
burst          pass          concurrent burst allowed 5 requests, limit is 5
recovery       pass          the next success was at or after the advertised reset

timeline 4.58s
+#x..........+#x........+.+............#............#x.....+
+ allowed   x rejected   # both   . idle

1 check failed
```

`boundary` failed: the server let through about twice the limit across a clock edge. Exit code is `1`.

A clean pass looks like this (`token` is a sliding window that holds):

```
./limitcheck demo --scenario token
```

```
scenario token
target http://127.0.0.1:43809/
limit 5 per 500ms
sent 55, allowed 37, rejected 18, errors 0, max drift 1ms

headers        pass          …
accuracy       pass          steady run allowed 5 requests, limit is 5
boundary       pass          second burst allowed 0 after the edge, limit is 5
burst          pass          concurrent burst allowed 5 requests, limit is 5
recovery       pass          the next success was at or after the advertised reset
isolation      pass          the second identity was still allowed, and a fresh request id did not raise the limit

no check failed
```

Other useful demos:

| Command | What you should see |
|---------|---------------------|
| `demo --scenario race` | `burst fail` — concurrent burst allowed 7, limit is 5 |
| `demo --scenario open` | `accuracy fail` — steady run allowed 7, limit is 5 |
| `demo --scenario bad-headers` | `headers fail` — rejected with status 503, want 429 |
| `demo --scenario early` | `recovery fail` — success before the advertised reset |

`fixed-window` and `racy` are older names for `fixed` and `race`. `correct` is an older name for `token`.

## Point it at your own server

Localhost needs no extra flag:

```
./limitcheck http://127.0.0.1:8080/v1/items \
  --limit 5/1s \
  --header 'Authorization: Bearer secret'
```

`--limit` is `count/window`, for example `100/60s` or `5/500ms`. The window must be at least 400ms.

Each check waits out one full window before measuring, so leftover traffic is not still counting. A 60s limit takes a few minutes. A limit of 100 per 60s plans hundreds of requests; the default cap is 200, so raise `--max-requests` if the plan is bigger than that.

## Safety: remote hosts

A real send to any host other than `127.0.0.1`, `::1`, or `localhost` is refused unless you pass `--yes`. The tool compares the host text and does not look up DNS.

```
./limitcheck http://203.0.113.5/items --limit 5/1s
```

```
limitcheck: 203.0.113.5 is not a local address, so pass --yes to send traffic there
```

Exit `2`. Nothing was sent. Add `--yes` only when you mean to hit that host.

## Preview without sending

`--dry-run` prints the wave plan and exits `0`. It never dials, so a public URL does not need `--yes`.

```
./limitcheck --dry-run --limit 5/1s http://203.0.113.5/items
```

```
dry run, 40 requests, nothing sent
limit 5 per 1s
headers        sample     7
accuracy       steady     7
boundary       pre        5
boundary       post       5
burst          burst      7
recovery       exhaust    7
recovery       follower   2
```

## What each check means

| Check | One sentence |
|-------|--------------|
| **headers** | Rejections are 429s with Retry-After and a rate-limit header, remaining does not climb, and the body has a reset time. |
| **accuracy** | A steady series is allowed about the count you named, not a lot more or less. |
| **boundary** | A burst just before a clock edge plus a burst just after does not get ~2× the limit. |
| **burst** | Overlapping requests do not sneak past the limit (inconclusive if the limit is 64 or higher). |
| **recovery** | After a 429, the next success is at or after the reset time the server advertised, not earlier. |
| **isolation** | Joins when you pass two `--identity` values or `--key-by`. One client filling the bucket must not throttle another; rotating `X-Limitcheck-Request` must not mint a new bucket. |
| **multi_instance** | Joins when you pass two `--origin` values and `--sharing`. Shared means one counter; without sharing, each origin keeping its own full limit fails this check. |

Default run order: headers, accuracy, boundary, burst, recovery. Isolation and multi_instance join only when the flags above are set (or you name them with `--check`).

## How to read a report

1. First lines: target, stated limit, how many were sent / allowed / rejected.
2. One line per check: name, status, short reason.
3. Timeline: `+` allowed, `x` rejected, `#` both in that slice, `.` idle.
4. Last line: how many checks failed.

Pass `--json` for machine-readable output. In CI, exit `1` fails the job.

If **headers** fails and an Unkey gateway fronts the API, compare with Unkey's [rate limited](https://www.unkey.com/docs/errors/frontline/client/rate_limited) 429: it sends `Retry-After` and `X-RateLimit-*`, and the JSON body needs a reset time for this check to pass.

## Config file

`limitcheck.yaml`:

```yaml
url: http://127.0.0.1:8080/v1/items
method: POST
body: '{"n":1}'
headers:
  Authorization: Bearer secret
limit: 10/1s
identities:
  - name: a
    headers:
      X-Identity: a
  - name: b
    headers:
      X-Identity: b
key_by:
  - X-Identity
origins:
  - http://127.0.0.1:8081/v1/items
sharing: true
checks:
  - accuracy
  - boundary
max_requests: 200
```

```
./limitcheck --config limitcheck.yaml --json
```

A flag you set on the command overrides that field from the file. Separate headers inside one `--identity` with `;` (for example `name=a; X-Identity: a`).

## Serve a broken limiter yourself

```
./limitcheck serve --addr 127.0.0.1:0 --limit 10/2s --algo fixed
```

`--algo` is `token` (holds), `fixed` (leaks across the clock edge), or `race` (lets overlapping requests through). The process prints the listen URL, then waits until you interrupt it.
