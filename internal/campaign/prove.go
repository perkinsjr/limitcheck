package campaign

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/perkinsjr/limitcheck/internal/brokenserver"
	"github.com/perkinsjr/limitcheck/internal/check"
	"github.com/perkinsjr/limitcheck/internal/render"
	"github.com/perkinsjr/limitcheck/internal/spec"
)

// Scenario is one built-in limiter and the verdicts it should produce.
type Scenario struct {
	Name       string
	Algo       string
	Identities int
	Sharing    bool
	Checks     []string
	// Want overrides the default that every verdict passes.
	Want map[string]check.Status
	Text map[string]string
}

// Scenarios is the proof matrix. Each check fails on a limiter that has its bug.
func Scenarios() []Scenario {
	return []Scenario{
		{Name: "token", Algo: "token", Identities: 2},
		{Name: "fixed", Algo: "fixed", Want: map[string]check.Status{"boundary": check.Fail}, Text: map[string]string{"boundary": "window edge"}},
		{Name: "race", Algo: "race", Checks: []string{"burst"}, Want: map[string]check.Status{"burst": check.Fail}},
		{Name: "per-instance", Algo: "per-instance", Sharing: true, Checks: []string{"multi_instance"}, Want: map[string]check.Status{"multi_instance": check.Fail}, Text: map[string]string{"multi_instance": "its own"}},
		{Name: "shared", Algo: "shared", Sharing: true, Identities: 2, Checks: []string{"multi_instance", "isolation"}, Want: map[string]check.Status{"multi_instance": check.Pass, "isolation": check.Fail}, Text: map[string]string{"isolation": "second identity", "multi_instance": "shared"}},
		{Name: "early", Algo: "early", Checks: []string{"recovery"}, Want: map[string]check.Status{"recovery": check.Fail}, Text: map[string]string{"recovery": "before the advertised reset"}},
		{Name: "bad-headers", Algo: "bad-headers", Checks: []string{"headers"}, Want: map[string]check.Status{"headers": check.Fail}, Text: map[string]string{"headers": "429"}},
		{Name: "open", Algo: "open", Checks: []string{"accuracy"}, Want: map[string]check.Status{"accuracy": check.Fail}, Text: map[string]string{"accuracy": "steady run allowed"}},
		{Name: "rotate", Algo: "rotate", Identities: 2, Checks: []string{"isolation"}, Want: map[string]check.Status{"isolation": check.Fail}, Text: map[string]string{"isolation": "X-Limitcheck-Request"}},
	}
}

// Alias maps older scenario names onto the algo list.
func Alias(name string) string {
	switch name {
	case "fixed-window":
		return "fixed"
	case "racy":
		return "race"
	case "correct":
		return "token"
	default:
		return name
	}
}

// Find returns a scenario by name or alias.
func Find(name string) (Scenario, bool) {
	name = Alias(name)
	for _, sc := range Scenarios() {
		if sc.Name == name || sc.Algo == name {
			return sc, true
		}
	}
	return Scenario{}, false
}

// Prove runs the scenario against a local broken server.
func Prove(ctx context.Context, sc Scenario) (render.Report, error) {
	rate := spec.Rate{Count: 5, Window: 500 * time.Millisecond}
	srv, err := brokenserver.Start(rate, sc.Algo)
	if err != nil {
		return render.Report{}, err
	}
	defer srv.Close()
	in := inputFor(sc, rate, srv.URLs)
	checks, err := check.Selected(in)
	if err != nil {
		return render.Report{}, err
	}
	return Run(ctx, in, checks)
}

func inputFor(sc Scenario, rate spec.Rate, urls []string) spec.Input {
	var origins []spec.Origin
	for _, raw := range urls {
		origins = append(origins, origin(raw))
	}
	ids := []spec.Identity{{Name: "default"}}
	if sc.Identities >= 2 {
		ids = []spec.Identity{
			{Name: "a", Headers: []spec.Header{{Name: "X-Identity", Value: "a"}}},
			{Name: "b", Headers: []spec.Header{{Name: "X-Identity", Value: "b"}}},
		}
	}
	caps := spec.DefaultCaps()
	caps.MaxRequests = 500
	return spec.Input{
		Method:     "GET",
		Policy:     spec.Policy{Rate: rate},
		Identities: ids,
		Origins:    origins,
		Checks:     sc.Checks,
		Sharing:    sc.Sharing,
		Caps:       caps,
		Yes:        true,
	}
}

func origin(raw string) spec.Origin {
	raw = strings.TrimPrefix(raw, "http://")
	host, path, _ := strings.Cut(raw, "/")
	if path == "" {
		path = "/"
	} else {
		path = "/" + path
	}
	return spec.Origin{Scheme: "http", Host: host, Path: path}
}

// Matches reports how the verdicts differ from the scenario.
func (sc Scenario) Matches(r render.Report) error {
	if len(r.Verdicts) == 0 {
		return fmt.Errorf("%s produced no verdicts", sc.Name)
	}
	seen := map[string]bool{}
	for _, v := range r.Verdicts {
		seen[v.Name] = true
		want := check.Pass
		if sc.Want != nil {
			if status, ok := sc.Want[v.Name]; ok {
				want = status
			}
		}
		if v.Status != want {
			return fmt.Errorf("%s %s was %s, want %s (%s)", sc.Name, v.Name, v.Status, want, v.Message)
		}
		if text := sc.Text[v.Name]; text != "" && !strings.Contains(v.Message, text) {
			return fmt.Errorf("%s %s message %q does not contain %q", sc.Name, v.Name, v.Message, text)
		}
	}
	for name := range sc.Want {
		if !seen[name] {
			return fmt.Errorf("%s did not run %s", sc.Name, name)
		}
	}
	return nil
}
