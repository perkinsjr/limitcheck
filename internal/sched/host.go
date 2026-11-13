package sched

import (
	"net"
	"strings"
)

func splitHost(hostport string) (host, port string, ok bool) {
	if strings.HasPrefix(hostport, "[") {
		h, p, err := net.SplitHostPort(hostport)
		if err != nil {
			return trimBrackets(hostport), "", false
		}
		return h, p, true
	}
	if strings.Count(hostport, ":") == 1 {
		h, p, err := net.SplitHostPort(hostport)
		if err != nil {
			return hostport, "", false
		}
		return h, p, true
	}
	return hostport, "", false
}

func trimBrackets(h string) string {
	return strings.Trim(h, "[]")
}

func parseIP(h string) net.IP {
	return net.ParseIP(h)
}
