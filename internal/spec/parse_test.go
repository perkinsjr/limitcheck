package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLimit(t *testing.T) {
	got, err := ParseLimit("100/60s")
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 100 || got.Window != 60*time.Second {
		t.Fatalf("got %+v", got)
	}
	got, err = ParseLimit("5 / 500ms")
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 5 || got.Window != 500*time.Millisecond {
		t.Fatalf("got %+v", got)
	}
	got, err = ParseLimit("10/1m")
	if err != nil {
		t.Fatal(err)
	}
	if got.Window != time.Minute {
		t.Fatalf("window %s", got.Window)
	}
	for _, raw := range []string{"", "5", "0/1s", "5/0s", "5/s", "-1/1s", "nope/1s"} {
		if _, err := ParseLimit(raw); err == nil {
			t.Fatalf("limit %q should fail", raw)
		}
	}
}

func TestParseRun(t *testing.T) {
	cmd, err := Parse([]string{
		"http://127.0.0.1:8080/v1/items",
		"--limit", "5/1s",
		"--header", "Authorization: Bearer t",
		"--identity", "name=alice; X-Identity: a",
		"--identity", "X-Identity: b",
		"--method", "post",
		"--body", `{"n":1}`,
		"--max-requests", "40",
		"--yes",
		"--dry-run",
		"--json",
		"--check", "accuracy,boundary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionRun {
		t.Fatalf("action %s", cmd.Action)
	}
	c := cmd.Campaign
	if c.Target.URL.Path != "/v1/items" || c.Target.Method != "POST" || c.Target.Body != `{"n":1}` {
		t.Fatalf("target %+v", c.Target)
	}
	if len(c.Target.Headers) != 1 || c.Target.Headers[0].Value != "Bearer t" {
		t.Fatalf("headers %+v", c.Target.Headers)
	}
	if c.Limit.Count != 5 || c.Limit.Window != time.Second {
		t.Fatalf("limit %+v", c.Limit)
	}
	if len(c.Identities) != 2 || c.Identities[0].Name != "alice" || c.Identities[1].Name != "client-2" {
		t.Fatalf("identities %+v", c.Identities)
	}
	if len(c.Origins) != 1 || c.Origins[0].Host != "127.0.0.1:8080" {
		t.Fatalf("origins %+v", c.Origins)
	}
	if len(c.Checks) != 2 || c.Checks[0] != CheckAccuracy || c.Checks[1] != CheckBoundary {
		t.Fatalf("checks %v", c.Checks)
	}
	if c.MaxRequests != 40 || !c.Yes || !c.DryRun || !c.JSON {
		t.Fatalf("flags %+v", c)
	}
	u, err := RequestURL(c, 0)
	if err != nil || u != "http://127.0.0.1:8080/v1/items" {
		t.Fatalf("request url %s %v", u, err)
	}
}

func TestParseDefaults(t *testing.T) {
	cmd, err := Parse([]string{"http://localhost/x", "--limit", "1/1s"})
	if err != nil {
		t.Fatal(err)
	}
	c := cmd.Campaign
	if c.Target.Method != "GET" || c.MaxRequests != DefaultMaxRequests || c.Yes || c.DryRun {
		t.Fatalf("defaults %+v", c)
	}
	if len(c.Identities) != 1 || c.Identities[0].Name != "default" {
		t.Fatalf("identity %+v", c.Identities)
	}
	if strings.Join(kindStrings(c.Checks), ",") != "accuracy,boundary,burst,isolation,headers,recovery" {
		t.Fatalf("checks %v", c.Checks)
	}
}

func TestParseOriginsSelectMulti(t *testing.T) {
	cmd, err := Parse([]string{
		"http://127.0.0.1:8080/v1/items?q=1",
		"--limit", "5/1s",
		"--origin", "http://127.0.0.1:8080",
		"--origin", "http://127.0.0.1:8081/ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Campaign.Origins) != 2 {
		t.Fatalf("origins %+v", cmd.Campaign.Origins)
	}
	if !HasCheck(cmd.Campaign, CheckMultiInstance) {
		t.Fatal("expected multi_instance")
	}
	u, err := RequestURL(cmd.Campaign, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u != "http://127.0.0.1:8081/v1/items?q=1" {
		t.Fatalf("url %s", u)
	}
}

func TestParseExplicitCheckSkipsDefaultMulti(t *testing.T) {
	cmd, err := Parse([]string{
		"http://127.0.0.1/x",
		"--limit", "1/1s",
		"--origin", "http://127.0.0.1:1",
		"--origin", "http://127.0.0.1:2",
		"--check", "burst",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Campaign.Checks) != 1 || cmd.Campaign.Checks[0] != CheckBurst {
		t.Fatalf("checks %v", cmd.Campaign.Checks)
	}
}

func TestParseRejects(t *testing.T) {
	cases := [][]string{
		{},
		{"http://127.0.0.1/x"},
		{"http://127.0.0.1/x", "--limit", "nope"},
		{"ftp://127.0.0.1/x", "--limit", "1/1s"},
		{"http://127.0.0.1/x", "--limit", "1/1s", "extra"},
		{"http://127.0.0.1/x", "--limit", "1/1s", "--check", "nope"},
		{"http://127.0.0.1/x", "--limit", "1/1s", "--max-requests", "0"},
		{"http://127.0.0.1/x", "--limit", "1/1s", "--identity", "not a header"},
	}
	for _, args := range cases {
		if _, err := Parse(args); err == nil {
			t.Fatalf("args %q should fail", strings.Join(args, " "))
		}
	}
	if _, err := Parse([]string{"--help"}); !errorsIsHelp(err) {
		t.Fatalf("help err %v", err)
	}
}

func TestParseConfigAndOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "limitcheck.yaml")
	text := `
url: http://127.0.0.1:9/from-file
method: PUT
body: hi
headers:
  X-From: file
limit: 9/2s
identities:
  - name: a
    headers:
      X-Identity: a
  - name: b
    headers:
      X-Identity: b
origins:
  - http://127.0.0.1:9
  - http://127.0.0.1:10
checks:
  - burst
max_requests: 15
yes: true
dry_run: true
json: false
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, err := Parse([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	c := cmd.Campaign
	if c.Target.URL.Path != "/from-file" || c.Target.Method != "PUT" || c.Limit.Count != 9 {
		t.Fatalf("from file %+v limit %+v", c.Target, c.Limit)
	}
	if !c.Yes || !c.DryRun || c.JSON || c.MaxRequests != 15 {
		t.Fatalf("flags %+v", c)
	}
	if len(c.Origins) != 2 || len(c.Identities) != 2 || len(c.Target.Headers) != 1 {
		t.Fatalf("shape origins %d ids %d headers %d", len(c.Origins), len(c.Identities), len(c.Target.Headers))
	}

	cmd, err = Parse([]string{
		"http://127.0.0.1:9/cli",
		"--config", path,
		"--limit", "3/1s",
		"--check", "accuracy",
		"--yes=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Campaign.Limit.Count != 3 || cmd.Campaign.Target.URL.Path != "/cli" {
		t.Fatalf("override %+v", cmd.Campaign)
	}
	if cmd.Campaign.Yes {
		t.Fatal("cli --yes=false should win")
	}
	if len(cmd.Campaign.Checks) != 1 || cmd.Campaign.Checks[0] != CheckAccuracy {
		t.Fatalf("checks %v", cmd.Campaign.Checks)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("url: http://127.0.0.1/x\nnope: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]string{"--config", bad, "--limit", "1/1s"}); err == nil {
		t.Fatal("unknown field should fail")
	}
}

func TestParseDemoAndSelfTest(t *testing.T) {
	cmd, err := Parse([]string{"demo", "--scenario", "fixed-window"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionDemo || cmd.Scenario != "fixed-window" {
		t.Fatalf("%+v", cmd)
	}
	cmd, err = Parse([]string{"--self-test", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != ActionSelfTest || !cmd.Campaign.JSON {
		t.Fatalf("%+v", cmd)
	}
	if _, err := Parse([]string{"demo", "http://127.0.0.1"}); err == nil {
		t.Fatal("demo url should fail")
	}
	if _, err := Parse([]string{"--self-test", "http://127.0.0.1"}); err == nil {
		t.Fatal("self-test url should fail")
	}
}

func kindStrings(kinds []CheckKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

func errorsIsHelp(err error) bool {
	return err == ErrHelp
}
