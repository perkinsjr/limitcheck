# limitcheck

You tell it the rate limit you expect. It hits the API with the traffic that would break a bad limiter, then tells you pass, warn, fail, or inconclusive for each check.

Exit codes: any fail → `1`; nothing failed but something was inconclusive → `2`; pass, warn, or dry-run → `0`; the run never started → `2`.

## How to use

Needs Go 1.22+.

```
git clone https://github.com/perkinsjr/limitcheck.git
cd limitcheck
go build -o limitcheck ./cmd/limitcheck
```

Against a local server:

```
./limitcheck http://127.0.0.1:8080/v1/items \
  --limit 5/1s \
  --header 'Authorization: Bearer secret'
```

`--limit` looks like `100/60s` or `5/500ms`. The window must be at least 400ms.

A host that is not `127.0.0.1`, `::1`, or `localhost` needs `--yes`, or the tool refuses and sends nothing:

```
./limitcheck http://203.0.113.5/items --limit 5/1s
```

```
limitcheck: 203.0.113.5 is not a local address, so pass --yes to send traffic there
```

`--dry-run` prints the plan and exits `0` without dialing. Public URLs do not need `--yes` for a dry run.

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

Save settings in `limitcheck.yaml` and run `./limitcheck --config limitcheck.yaml`. Flags you set on the command override the file. Pass `--json` for machine-readable output.

## Examples

Prove the built-in limiters first:

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

**Fail** — fixed window leaks across a clock edge:

```
./limitcheck demo --scenario fixed
```

```
scenario fixed
target http://127.0.0.1:35471/
limit 5 per 500ms
sent 40, allowed 31, rejected 9, errors 0, max drift 2ms

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

Exit code `1`. Boundary saw about twice the limit across the edge.

**Pass** — sliding window holds:

```
./limitcheck demo --scenario token
```

```
scenario token
target http://127.0.0.1:46347/
limit 5 per 500ms
sent 55, allowed 37, rejected 18, errors 0, max drift 2ms

headers        pass          429 responses include Retry-After, a rate-limit header, and a JSON reset, and remaining does not go up
accuracy       pass          steady run allowed 5 requests, limit is 5
boundary       pass          second burst allowed 0 after the edge, limit is 5
burst          pass          concurrent burst allowed 5 requests, limit is 5
recovery       pass          the next success was at or after the advertised reset
isolation      pass          the second identity was still allowed, and a fresh request id did not raise the limit

timeline 6.666s
+x......++x......+x.......+#........#....+.......#+.......+#
+ allowed   x rejected   # both   . idle

no check failed
```

Exit code `0`.

Serve a broken limiter yourself with `./limitcheck serve --addr 127.0.0.1:0 --limit 10/2s --algo fixed` (`token`, `fixed`, or `race`).

## What each check means

| Check | Meaning |
|-------|---------|
| **headers** | Rejections are 429s with Retry-After and a rate-limit header, remaining does not climb, and the body has a reset time. |
| **accuracy** | A steady series is allowed about the count you named. |
| **boundary** | Bursts just before and just after a clock edge do not get ~2× the limit. |
| **burst** | Overlapping requests do not sneak past the limit (inconclusive if the limit is 64 or higher). |
| **recovery** | After a 429, the next success is at or after the reset time the server advertised. |
| **isolation** | Joins when you pass two `--identity` values or `--key-by`. One client filling the bucket must not throttle another. |
| **multi_instance** | Joins when you pass two `--origin` values and `--sharing`. The origins should share one counter. |

Default order: headers, accuracy, boundary, burst, recovery. Isolation and multi_instance join only when those flags are set (or you name them with `--check`).

If headers fails and Unkey fronts the API, compare with Unkey's [rate limited](https://www.unkey.com/docs/errors/frontline/client/rate_limited) 429 shape — this check wants a reset time in the JSON body too.

## How to read a report

1. Target URL and the limit you stated.
2. Counts: sent, allowed, rejected, errors, max drift.
3. One line per check: name, status, short reason.
4. Timeline: `+` allowed, `x` rejected, `#` both in that slice, `.` idle.
5. Last line: how many checks failed.

In CI, exit `1` fails the job. Exit `2` means the run was inconclusive or never started — treat that as a problem too if you want a hard gate.

## How to use in CI

This workflow builds the binary, proves the tool against its own broken servers, then fails the job if a real target fails a check. Set `LIMITCHECK_URL`, `LIMITCHECK_LIMIT`, and optional auth in repository secrets/vars. Drop the real-target step until you have a loopback or staging host you are willing to hit with `--yes`.

```yaml
# .github/workflows/limitcheck.yml
name: limitcheck

on:
  push:
  pull_request:

jobs:
  rate-limit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"

      - name: Build
        run: go build -o limitcheck ./cmd/limitcheck

      - name: Self-test
        run: ./limitcheck --self-test

      - name: Check staging limit
        env:
          URL: ${{ vars.LIMITCHECK_URL }}
          LIMIT: ${{ vars.LIMITCHECK_LIMIT }}
          TOKEN: ${{ secrets.LIMITCHECK_TOKEN }}
        run: |
          set -euo pipefail
          if [ -z "${URL:-}" ] || [ -z "${LIMIT:-}" ]; then
            echo "Set LIMITCHECK_URL and LIMITCHECK_LIMIT to run against a real host."
            exit 0
          fi
          ./limitcheck "$URL" \
            --limit "$LIMIT" \
            --header "Authorization: Bearer $TOKEN" \
            --yes \
            --max-requests 500
```

A one-off script does the same thing locally:

```bash
#!/usr/bin/env bash
set -euo pipefail
go build -o limitcheck ./cmd/limitcheck
./limitcheck --self-test
./limitcheck "$LIMITCHECK_URL" --limit "$LIMITCHECK_LIMIT" --yes
```

Because a failed check exits `1`, `set -e` (and GitHub Actions) stop the job there.
