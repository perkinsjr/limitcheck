package safety

import (
	"testing"
	"time"

	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestGateLocalAndRemote(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	local := mustCampaign(t, "http://127.0.0.1:9/x", false, 200)
	p, err := plan.Build(local, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Gate(local, p); err != nil {
		t.Fatal(err)
	}
	if err := Gate(local, p); err != nil {
		t.Fatal("gate changed on a second call")
	}

	for _, raw := range []string{
		"http://localhost/x",
		"http://localhost:8080/x",
		"http://[::1]/x",
		"http://[::1]:8080/x",
	} {
		c := mustCampaign(t, raw, false, 200)
		p, err := plan.Build(c, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := Gate(c, p); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}

	remote := mustCampaign(t, "http://203.0.113.5/items", false, 200)
	p, err = plan.Build(remote, now)
	if err != nil {
		t.Fatal(err)
	}
	err = Gate(remote, p)
	if err == nil {
		t.Fatal("remote host should be refused")
	}
	if err2 := Gate(remote, p); err2 == nil || err2.Error() != err.Error() {
		t.Fatal("gate is not stable")
	}
	remote.Yes = true
	if err := Gate(remote, p); err != nil {
		t.Fatal(err)
	}
}

func TestGateMaxRequests(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := mustCampaign(t, "http://127.0.0.1/x", false, 5)
	p, err := plan.Build(c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Slots) <= 5 {
		t.Fatalf("plan has %d slots, want more than 5", len(p.Slots))
	}
	if err := Gate(c, p); err == nil {
		t.Fatal("over the max should fail")
	}
	c.MaxRequests = len(p.Slots)
	if err := Gate(c, p); err != nil {
		t.Fatal(err)
	}
}

func mustCampaign(t *testing.T, raw string, yes bool, max int) spec.Campaign {
	t.Helper()
	args := []string{raw, "--limit", "5/1s", "--max-requests", itoa(max), "--check", "burst"}
	if yes {
		args = append(args, "--yes")
	}
	cmd, err := spec.Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Campaign
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
