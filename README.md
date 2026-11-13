# Check whether a rate limit holds

limitcheck sends a planned series of HTTP requests and reports whether the limit you named is the limit the server enforces. Each check is pass, warn, or fail. If any check fails, the process exits with code 1, so a CI job can stop on it.

## Run it against a local server

Build the binary from this repo.

```
go build -o limitcheck ./cmd/limitcheck
```

Point it at a URL on this machine and name the limit as a count and a duration.

```
./limitcheck http://127.0.0.1:8080/v1/items --limit 100/60s --header 'Authorization: Bearer secret'
```

You get one line per check, then a timeline. `+` means the server accepted the request. `x` means the server rejected it. `#` means that slice of the run had both. `.` means nothing was sent then.

A full run spreads traffic across a few windows. A 60s limit takes a few minutes. It also sends more requests than the default cap of 200. When the plan is over the cap, the command exits 2 and tells you to raise `--max-requests`.

The window has to be at least 400ms.

## Refuse a host that is not local

A host other than `localhost`, `127.0.0.1`, or `::1` is refused unless you pass `--yes`. Nothing is sent.

```
./limitcheck http://203.0.113.5/items --limit 5/1s
```

The error names the host and `--yes`. The exit code is 2.

Pass `--yes` only when you intend to send that traffic.

## Preview the plan

`--dry-run` prints every planned send time and sends nothing.

```
./limitcheck http://127.0.0.1:8080/v1/items --limit 5/1s --dry-run
```

The first line gives the request count and says nothing was sent. The exit code is 0 when the plan is allowed. A remote host still needs `--yes`, because the command refuses that host before it prints the plan.

## Read a failure

**accuracy.** The steady series was allowed a count that is not close to the limit you named.

**boundary.** A burst just before a clock edge and a burst just after it were both allowed, so the server accepted about twice the limit inside one window. The edge is your window length stepped along the system clock. If the server resets on a different schedule, the bursts can miss that edge. The check warns that it could not line them up.

**burst.** Requests sent at the same moment were allowed more times than the limit.

**isolation.** After the first client filled the limit, a second client was rejected too. A new `X-Limitcheck-Request` on every call fails this check when the server treats that header as a new client. Pass a second `--identity` to test two clients. Separate extra headers in one identity with `;`.

**headers.** A rejection used a status other than 429. A 429 had no `Retry-After`, or it had neither a `RateLimit` header nor an `X-RateLimit-*` header. The remaining count went up across the steady requests. The 429 body was not JSON with a reset time.

**recovery.** A request sent after a 429 succeeded before the reset time the server advertised.

**multi_instance.** Each `--origin` kept its own counter and allowed about a full limit. The check is added for you when you pass two origins and you do not pick checks yourself.

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
origins:
  - http://127.0.0.1:8080
  - http://127.0.0.1:8081
checks:
  - accuracy
  - boundary
  - burst
max_requests: 200
```

```
./limitcheck --config limitcheck.yaml --json
```

A flag you set on the command replaces that field from the file. `--json` prints the report as JSON on standard output.

To send a second method or body from the command line, pass `--method` and `--body`.

## Run the built-in broken servers

The command includes small servers that break one rule each.

```
./limitcheck --self-test
```

The command exits 0 when every broken server failed the check that matches its bug, and the correct server passed. `fixed-window` fails boundary. `racy` fails burst. `per-instance` fails multi_instance.

To read the full report for one server, name it.

```
./limitcheck demo --scenario fixed-window
```

That report fails the boundary check, so the exit code is 1. A local run allowed 10 requests across the edge when the limit was 5.
