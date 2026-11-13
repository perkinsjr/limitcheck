// Package config reads flags and YAML into a spec.Input.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/perkinsjr/limitcheck/internal/spec"
	"gopkg.in/yaml.v3"
)

// ErrHelp means the user asked for the usage text.
var ErrHelp = errors.New("help")

// Usage is the help text.
func Usage() string {
	return `limitcheck checks whether an HTTP rate limit behaves the way you described.

  limitcheck <url> --limit 100/60s --header "Authorization: Bearer ..."
  limitcheck --config limitcheck.yaml
  limitcheck --dry-run --limit 100/60s https://example.com/items
  limitcheck --self-test
  limitcheck demo --scenario fixed
  limitcheck serve --addr 127.0.0.1:0 --limit 10/2s --algo token

Flags
  --limit N/Ds       the limit you expect, such as 100/60s or 5/500ms
  --header H: V      header sent on every request (repeatable)
  --identity ...     one caller, as name=alice; X-Identity: a (repeatable)
  --origin URL       another host (repeatable)
  --check NAME       run one check (repeatable). Default is headers, accuracy,
                     boundary, burst, recovery. isolation joins when two
                     identities or --key-by are set. multi_instance joins when
                     two origins and --sharing are set.
  --key-by HEADER    header that should separate callers (repeatable)
  --sharing          expect the origins to share one limit
  --method METHOD    HTTP method (default GET)
  --body TEXT        request body
  --max-requests N   hard cap, default 200
  --yes              send to a host that is not local
  --dry-run          print the waves and send nothing
  --json             print JSON
  --config PATH      YAML file
  --self-test        prove each check against a broken local limiter

A real send to a host that is not 127.0.0.1, ::1, or localhost needs --yes.
A dry run does not send, so it does not need --yes.
The process exits 1 when a check fails, and 2 when a check is inconclusive
or the run does not start.
`
}

// Parse reads argv after the program name.
func Parse(args []string) (spec.Command, error) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		return spec.Command{Action: spec.ActionHelp}, nil
	}
	if len(args) > 0 && args[0] == "demo" {
		return parseDemo(args[1:])
	}
	if len(args) > 0 && args[0] == "serve" {
		return parseServe(args[1:])
	}
	return parseRun(args)
}

func parseDemo(args []string) (spec.Command, error) {
	fs := newFlagSet()
	scenario := fs.String("scenario", "", "one built-in limiter")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return spec.Command{}, ErrHelp
		}
		return spec.Command{}, err
	}
	if fs.NArg() != 0 {
		return spec.Command{}, fmt.Errorf("demo does not take a url")
	}
	return spec.Command{Action: spec.ActionDemo, Scenario: strings.TrimSpace(*scenario), Input: spec.Input{JSON: *jsonOut}}, nil
}

func parseServe(args []string) (spec.Command, error) {
	fs := newFlagSet()
	addr := fs.String("addr", "127.0.0.1:0", "listen address")
	limitFlag := fs.String("limit", "", "limit the server should enforce")
	algo := fs.String("algo", "", "token, fixed, or race")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return spec.Command{}, ErrHelp
		}
		return spec.Command{}, err
	}
	if fs.NArg() != 0 {
		return spec.Command{}, fmt.Errorf("serve does not take a url")
	}
	if *limitFlag == "" {
		return spec.Command{}, fmt.Errorf("serve needs --limit")
	}
	rate, err := spec.ParseRate(*limitFlag)
	if err != nil {
		return spec.Command{}, err
	}
	switch *algo {
	case "token", "fixed", "race":
	default:
		return spec.Command{}, fmt.Errorf("serve --algo must be token, fixed, or race")
	}
	return spec.Command{
		Action: spec.ActionServe,
		Addr:   *addr,
		Algo:   *algo,
		Input:  spec.Input{Policy: spec.Policy{Rate: rate}},
	}, nil
}

func parseRun(args []string) (spec.Command, error) {
	fs := newFlagSet()
	limitFlag := fs.String("limit", "", "intended limit")
	methodFlag := fs.String("method", "", "HTTP method")
	bodyFlag := fs.String("body", "", "request body")
	maxFlag := fs.Int("max-requests", spec.DefaultMaxRequests, "hard cap")
	yesFlag := fs.Bool("yes", false, "allow a host that is not local")
	dryFlag := fs.Bool("dry-run", false, "print the plan and send nothing")
	jsonFlag := fs.Bool("json", false, "print JSON")
	configFlag := fs.String("config", "", "YAML file")
	selfTest := fs.Bool("self-test", false, "run the built-in broken limiters")
	sharingFlag := fs.Bool("sharing", false, "expect origins to share one limit")
	var headers, identities, origins, checks, keyBy []string
	fs.Func("header", "header", func(s string) error { headers = append(headers, s); return nil })
	fs.Func("identity", "identity", func(s string) error { identities = append(identities, s); return nil })
	fs.Func("origin", "origin", func(s string) error { origins = append(origins, s); return nil })
	fs.Func("check", "check", func(s string) error { checks = append(checks, s); return nil })
	fs.Func("key-by", "key header", func(s string) error { keyBy = append(keyBy, s); return nil })
	if err := fs.Parse(reorderArgs(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return spec.Command{}, ErrHelp
		}
		return spec.Command{}, err
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flagSetter) { visited[f.name] = true })

	if *selfTest {
		if fs.NArg() != 0 {
			return spec.Command{}, fmt.Errorf("--self-test does not take a url")
		}
		return spec.Command{Action: spec.ActionSelfTest, Input: spec.Input{JSON: *jsonFlag}}, nil
	}

	var file *fileConfig
	if *configFlag != "" {
		f, err := loadConfig(*configFlag)
		if err != nil {
			return spec.Command{}, err
		}
		file = f
	}

	rawURL := ""
	switch fs.NArg() {
	case 0:
		if file != nil {
			rawURL = file.URL
		}
	case 1:
		rawURL = fs.Arg(0)
	default:
		return spec.Command{}, fmt.Errorf("pass one url")
	}
	if rawURL == "" {
		return spec.Command{}, fmt.Errorf("pass a url or --config")
	}
	primary, err := parseHTTPURL(rawURL)
	if err != nil {
		return spec.Command{}, err
	}

	limitRaw := ""
	if file != nil {
		limitRaw = file.Limit
	}
	if visited["limit"] || limitRaw == "" {
		if *limitFlag != "" {
			limitRaw = *limitFlag
		}
	}
	if limitRaw == "" {
		return spec.Command{}, fmt.Errorf("pass --limit, for example 100/60s")
	}
	rate, err := spec.ParseRate(limitRaw)
	if err != nil {
		return spec.Command{}, err
	}

	method := "GET"
	if file != nil && file.Method != "" {
		method = file.Method
	}
	if visited["method"] {
		method = *methodFlag
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return spec.Command{}, fmt.Errorf("method is empty")
	}

	body := ""
	if file != nil {
		body = file.Body
	}
	if visited["body"] {
		body = *bodyFlag
	}

	var baseHeaders []spec.Header
	if file != nil {
		baseHeaders, err = headersFromMap(file.Headers)
		if err != nil {
			return spec.Command{}, err
		}
	}
	if len(headers) > 0 {
		baseHeaders, err = parseHeaderFlags(headers)
		if err != nil {
			return spec.Command{}, err
		}
	}

	var ids []spec.Identity
	if file != nil {
		for i, raw := range file.Identities {
			h, err := headersFromMap(raw.Headers)
			if err != nil {
				return spec.Command{}, err
			}
			name := strings.TrimSpace(raw.Name)
			if name == "" {
				name = fmt.Sprintf("client-%d", i+1)
			}
			ids = append(ids, spec.Identity{Name: name, Headers: h})
		}
	}
	if len(identities) > 0 {
		ids, err = parseIdentityFlags(identities)
		if err != nil {
			return spec.Command{}, err
		}
	}
	if len(ids) == 0 {
		ids = []spec.Identity{{Name: "default"}}
	}

	originList := []spec.Origin{originFrom(primary, primary)}
	var rawOrigins []string
	if file != nil && len(origins) == 0 {
		rawOrigins = file.Origins
	}
	if len(origins) > 0 {
		rawOrigins = origins
	}
	for _, raw := range rawOrigins {
		u, err := parseHTTPURL(raw)
		if err != nil {
			return spec.Command{}, fmt.Errorf("origin %q: %w", raw, err)
		}
		next := originFrom(primary, u)
		if !sameOrigin(originList, next) {
			originList = append(originList, next)
		}
	}

	var kindNames []string
	if file != nil && len(checks) == 0 {
		kindNames = file.Checks
	}
	if len(checks) > 0 {
		for _, c := range checks {
			for _, part := range strings.Split(c, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					kindNames = append(kindNames, part)
				}
			}
		}
	}

	var keys []string
	if file != nil && len(keyBy) == 0 {
		keys = append(keys, file.KeyBy...)
	}
	if len(keyBy) > 0 {
		keys = keyBy
	}

	maxRequests := spec.DefaultMaxRequests
	if file != nil && file.MaxRequests != nil {
		maxRequests = *file.MaxRequests
	}
	if visited["max-requests"] {
		maxRequests = *maxFlag
	}
	if maxRequests < 1 {
		return spec.Command{}, fmt.Errorf("--max-requests must be at least 1")
	}

	yes := false
	if file != nil && file.Yes != nil {
		yes = *file.Yes
	}
	if visited["yes"] {
		yes = *yesFlag
	}
	dry := false
	if file != nil && file.DryRun != nil {
		dry = *file.DryRun
	}
	if visited["dry-run"] {
		dry = *dryFlag
	}
	jsonOut := false
	if file != nil && file.JSON != nil {
		jsonOut = *file.JSON
	}
	if visited["json"] {
		jsonOut = *jsonFlag
	}
	sharing := false
	if file != nil && file.Sharing != nil {
		sharing = *file.Sharing
	}
	if visited["sharing"] {
		sharing = *sharingFlag
	}

	caps := spec.DefaultCaps()
	caps.MaxRequests = maxRequests
	return spec.Command{
		Action: spec.ActionRun,
		Input: spec.Input{
			Method: method, Body: body, Headers: baseHeaders,
			Policy:     spec.Policy{Rate: rate},
			Identities: ids,
			Origins:    originList,
			Checks:     kindNames,
			KeyBy:      keys,
			Sharing:    sharing,
			Caps:       caps,
			Yes:        yes,
			DryRun:     dry,
			JSON:       jsonOut,
		},
	}, nil
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("url %q must be http or https", raw)
	}
	return u, nil
}

func originFrom(primary, u *url.URL) spec.Origin {
	path := u.Path
	query := u.RawQuery
	if primary != nil && (path == "" || path == "/") && u != primary {
		path = primary.Path
		if query == "" {
			query = primary.RawQuery
		}
	}
	return spec.Origin{Scheme: u.Scheme, Host: u.Host, Path: path, RawQuery: query}
}

func sameOrigin(have []spec.Origin, next spec.Origin) bool {
	for _, o := range have {
		if o.Scheme == next.Scheme && o.Host == next.Host {
			return true
		}
	}
	return false
}

func parseIdentityFlags(raws []string) ([]spec.Identity, error) {
	var out []spec.Identity
	for _, raw := range raws {
		id, err := spec.ParseIdentity(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func parseHeaderFlags(raws []string) ([]spec.Header, error) {
	var out []spec.Header
	for _, raw := range raws {
		h, err := parseOneHeader(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func parseOneHeader(raw string) (spec.Header, error) {
	name, value, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(name) == "" {
		return spec.Header{}, fmt.Errorf("header %q needs a colon", raw)
	}
	return spec.Header{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)}, nil
}

func headersFromMap(m map[string]string) ([]spec.Header, error) {
	var out []spec.Header
	for name, value := range m {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header name is empty")
		}
		out = append(out, spec.Header{Name: name, Value: value})
	}
	return out, nil
}

type fileConfig struct {
	URL         string            `yaml:"url"`
	Method      string            `yaml:"method"`
	Body        string            `yaml:"body"`
	Headers     map[string]string `yaml:"headers"`
	Limit       string            `yaml:"limit"`
	Identities  []fileIdentity    `yaml:"identities"`
	Origins     []string          `yaml:"origins"`
	Checks      []string          `yaml:"checks"`
	MaxRequests *int              `yaml:"max_requests"`
	Yes         *bool             `yaml:"yes"`
	DryRun      *bool             `yaml:"dry_run"`
	JSON        *bool             `yaml:"json"`
	KeyBy       []string          `yaml:"key_by"`
	Sharing     *bool             `yaml:"sharing"`
}

type fileIdentity struct {
	Name    string            `yaml:"name"`
	Headers map[string]string `yaml:"headers"`
}

func loadConfig(path string) (*fileConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decodeConfig(f)
}

func decodeConfig(r io.Reader) (*fileConfig, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var cfg fileConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}
