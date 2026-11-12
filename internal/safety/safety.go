// Package safety decides whether a sealed plan is allowed to send.
package safety

import (
	"fmt"
	"net"
	"strings"

	"github.com/perkinsjr/limitcheck/internal/plan"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Gate is pure. The same plan and campaign always return the same error.
// It does not open a connection.
func Gate(c spec.Campaign, p plan.Plan) error {
	if len(p.Slots) == 0 {
		return fmt.Errorf("plan has no requests")
	}
	if c.MaxRequests < 1 {
		return fmt.Errorf("max requests must be at least 1")
	}
	if len(p.Slots) > c.MaxRequests {
		return fmt.Errorf("plan sends %d requests, which is over the max of %d", len(p.Slots), c.MaxRequests)
	}
	if c.Yes {
		return nil
	}
	seen := map[string]bool{}
	for _, origin := range c.Origins {
		if seen[origin.Host] {
			continue
		}
		seen[origin.Host] = true
		if !loopback(origin.Host) {
			return fmt.Errorf("%s is not a local address, so pass --yes to send traffic there", origin.Host)
		}
	}
	return nil
}

func loopback(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	low := strings.ToLower(host)
	if low == "localhost" || strings.HasSuffix(low, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
