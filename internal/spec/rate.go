package spec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseRate reads N/<duration>, for example 100/60s or 5/500ms.
func ParseRate(s string) (Rate, error) {
	s = strings.TrimSpace(s)
	n, rest, ok := strings.Cut(s, "/")
	if !ok || strings.TrimSpace(n) == "" || strings.TrimSpace(rest) == "" {
		return Rate{}, fmt.Errorf("limit %q must look like 100/60s", s)
	}
	count, err := strconv.Atoi(strings.TrimSpace(n))
	if err != nil || count < 1 {
		return Rate{}, fmt.Errorf("limit %q needs a count of at least 1", s)
	}
	window, err := time.ParseDuration(strings.TrimSpace(rest))
	if err != nil || window <= 0 {
		return Rate{}, fmt.Errorf("limit %q has no positive window", s)
	}
	return Rate{Count: count, Window: window}, nil
}

// ParseIdentity reads name=alice; Header: value; Header: value.
func ParseIdentity(s string) (Identity, error) {
	parts := splitSemi(s)
	if len(parts) == 0 {
		return Identity{}, fmt.Errorf("identity %q is empty", s)
	}
	name, value, ok := strings.Cut(parts[0], "=")
	if !ok || strings.TrimSpace(name) != "name" || strings.TrimSpace(value) == "" {
		return Identity{}, fmt.Errorf("identity %q must start with name=<name>", s)
	}
	id := Identity{Name: strings.TrimSpace(value)}
	for _, p := range parts[1:] {
		h, v, ok := strings.Cut(p, ":")
		if !ok || strings.TrimSpace(h) == "" {
			return Identity{}, fmt.Errorf("identity header %q needs a colon", p)
		}
		id.Headers = append(id.Headers, Header{Name: strings.TrimSpace(h), Value: strings.TrimSpace(v)})
	}
	return id, nil
}

func splitSemi(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
