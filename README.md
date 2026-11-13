# Check whether a rate limit holds

limitcheck sends HTTP requests and reports whether the limit you named is the limit the server enforces. A check is pass, warn, fail, or inconclusive. If any check fails, the process exits 1, so CI stops the job.

## Run it against a local server

Build the binary from this repo.

```
go build -o limitcheck ./cmd/limitcheck
```

Point it at a URL on this machine and name the limit as a count and a duration.

```
./limitcheck http://127.0.0.1:8080/v1/items --limit 100/60s --header 'Authorization: Bearer secret'
```

The report prints one line per check, then a timeline. `+` means the server accepted the request. `x` means the server rejected it. `#` means that slice of the run had both. `.` means nothing was sent then.

Each check waits out one full window before it measures, so earlier traffic is not still inside the window. A 60s limit takes a few minutes. A limit of 100 per 60s plans 646 requests. The default cap is 200, so that command exits 2 until you raise `--max-requests`.

The window has to be at least 400ms.

## Refuse a host that is not local

A real send to a host other than `localhost`, `127.0.0.1`, or `::1` is refused unless you pass `--yes`. Nothing is sent. The command compares the host text and does not resolve DNS, so a name that points at this machine is still refused.

```
./limitcheck http://203.0.113.5/items --limit 5/1s
```

The error names the host and `--yes`. The exit code is 2.

Pass `--yes` only when you intend to send that traffic.

## Preview the plan

`--dry-run` prints each wave and its request count, then sends nothing. A dry run does not connect, so a public URL does not need `--yes`.

```
./limitcheck --dry-run --limit 5/1s http://203.0.113.5/items
```

The first line gives the request count and says nothing was sent. The exit code is 0 when the plan is under the cap.

## Read a failure

**accuracy.** The steady series was allowed a count that is not close to the limit you named.

**boundary.** A burst just before a clock edge and a burst just after it were both allowed, so the server accepted about twice the limit. The edge is your window length stepped along the system clock. If those bursts miss the edge, the check is inconclusive and the exit code is 2, unless another check failed.

**burst.** Requests sent at the same moment were allowed more times than the limit. At most 64 run at once. If the limit is 64 or higher, this check is inconclusive, because a race below that cap would still be under the limit.

**isolation.** This joins the run when you pass two `--identity` values, or `--key-by`. After the first client filled the limit, a second client was rejected too. A new `X-Limitcheck-Request` on every call fails this check when the server treats that header as a new client. Separate extra headers in one identity with `;`.

**headers.** A rejection used a status other than 429. A 429 had no `Retry-After`, or it had neither a `RateLimit` header nor an `X-RateLimit-*` header. The remaining count went up across the allowed requests. The 429 body was not JSON with a reset time.

**recovery.** A request sent after a 429 succeeded before the reset time the server advertised. The tool reads that time from the 429, then sends one follow-up. It does not schedule a second follow-up from that reply.

**multi_instance.** Each `--origin` kept its own counter and allowed about a full limit. The check joins the run when you pass two origins and `--sharing`.

If the headers check fails on an API that Unkey fronts, compare the body with the gateway [rate limited](https://www.unkey.com/docs/errors/frontline/client/rate_limited) error. That 429 sends `Retry-After`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`. The JSON body carries `error.code`. The reset time is only in `X-RateLimit-Reset`. This check fails until the body includes a reset time.

## Save the settings in a file

Create `limitcheck.yaml`.

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

A flag you set on the command replaces that field from the file. `--json` prints the report as JSON on standard output. Pass `--method` and `--body` to set those from the command line.

The `origins` list adds hosts next to `url`. Two origins and `sharing: true` turn on `multi_instance` when you do not name checks yourself.

## Run the built-in broken servers

The command includes small servers that break one rule each.

```
./limitcheck --self-test
```

The command exits 0 when every broken server failed the check that matches its bug, and the token limiter passed. `fixed` fails boundary. `race` fails burst. `per-instance` fails multi_instance. `fixed-window` and `racy` are older names for `fixed` and `race`.

To read the full report for one server, name it.

```
./limitcheck demo --scenario fixed
```

That report fails the boundary check, so the exit code is 1. A local run allowed 10 requests across the edge when the limit was 5.

To serve one limiter yourself, pick `token`, `fixed`, or `race`.

```
./limitcheck serve --addr 127.0.0.1:0 --limit 10/2s --algo fixed
```

`token` keeps a sliding window. `fixed` resets on a clock boundary and allows another full limit on the far side. `race` lets overlapping requests through.
