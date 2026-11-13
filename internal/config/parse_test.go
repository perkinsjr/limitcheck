package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestURLBeforeFlags(t *testing.T) {
	cmd, err := Parse([]string{"http://127.0.0.1:9/check", "--limit", "5/1s", "--header", "A: b"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Input.Origins[0].Path != "/check" || cmd.Input.Policy.Rate.Count != 5 {
		t.Fatalf("%+v", cmd.Input)
	}
	if len(cmd.Input.Headers) != 1 || cmd.Input.Headers[0].Value != "b" {
		t.Fatal(cmd.Input.Headers)
	}
}

func TestFlagOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "limitcheck.yaml")
	body := []byte("url: http://127.0.0.1:9/from-file\nlimit: 9/1s\nyes: true\nunknown: 1\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]string{"--config", path}); err == nil {
		t.Fatal("unknown field")
	}
	body = []byte("url: http://127.0.0.1:9/from-file\nlimit: 9/1s\nyes: true\norigins:\n  - http://127.0.0.1:10/from-file\nsharing: true\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, err := Parse([]string{"--config", path, "--limit", "5/1s", "--yes=false"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Input.Policy.Rate.Count != 5 || cmd.Input.Yes {
		t.Fatalf("%+v", cmd.Input)
	}
	if len(cmd.Input.Origins) != 2 || !cmd.Input.Sharing {
		t.Fatalf("%+v", cmd.Input.Origins)
	}
}

func TestDryRunRemoteParses(t *testing.T) {
	cmd, err := Parse([]string{"--dry-run", "--limit", "5/1s", "http://203.0.113.5/items"})
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.Input.DryRun || cmd.Input.Yes {
		t.Fatal(cmd.Input)
	}
}

func TestServeAndSelfTest(t *testing.T) {
	cmd, err := Parse([]string{"serve", "--limit", "10/2s", "--algo", "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != spec.ActionServe || cmd.Algo != "fixed" {
		t.Fatal(cmd)
	}
	if _, err := Parse([]string{"serve", "--limit", "10/2s", "--algo", "nope"}); err == nil {
		t.Fatal("bad algo")
	}
	cmd, err = Parse([]string{"--self-test"})
	if err != nil || cmd.Action != spec.ActionSelfTest {
		t.Fatal(cmd, err)
	}
	cmd, err = Parse([]string{"--help"})
	if err != nil || cmd.Action != spec.ActionHelp {
		t.Fatal(cmd, err)
	}
	if strings.Contains(Usage(), "—") {
		t.Fatal("em dash in help")
	}
}
