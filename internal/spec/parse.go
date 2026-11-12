package spec

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrHelp means the user asked for usage text.
var ErrHelp = errors.New("help")

// Usage is the command help.
func Usage() string {
	return strings.TrimSpace(`
limitcheck sends a planned burst of traffic and reports whether a rate limit holds.

  limitcheck <url> --limit N/Ds [flags]
  limitcheck --config limitcheck.yaml
  limitcheck demo --scenario fixed-window
  limitcheck --self-test

Flags
  --limit N/Ds       intended limit, such as 100/60s or 5/1s
  --header 'K: V'    header sent on every request (repeatable)
  --identity 'K: V'  one client, as headers (repeatable, ';' separates headers)
  --origin URL       extra host for the shared-limit check (repeatable)
  --check NAME       which check to run (repeatable). Default is all except multi_instance
  --method METHOD    HTTP method (default GET)
  --body TEXT        request body
  --max-requests N   hard cap on planned requests (default 200)
  --yes              allow a host that is not loopback
  --dry-run          print the plan and send nothing
  --json             print the report as JSON
  --config PATH      YAML file. Flags that you set override the file
  --self-test        run the built-in broken limiters and prove each check
`) + "\n"
}

// Parse reads args into a Command. args does not include the program name.
func Parse(args []string) (Command, error) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		return Command{}, ErrHelp
	}
	if len(args) > 0 && args[0] == "demo" {
		return parseDemo(args[1:])
	}
	return parseRun(args)
}

func parseDemo(args []string) (Command, error) {
	fs := newFlagSet()
	scenario := fs.String("scenario", "", "one built-in limiter, or all of them when empty")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return Command{}, ErrHelp
		}
		return Command{}, err
	}
	if fs.NArg() != 0 {
		return Command{}, fmt.Errorf("demo does not take a url")
	}
	return Command{
		Action:   ActionDemo,
		Scenario: strings.TrimSpace(*scenario),
		Campaign: Campaign{JSON: *jsonOut, MaxRequests: DefaultMaxRequests, Yes: true},
	}, nil
}

func parseRun(args []string) (Command, error) {
	fs := newFlagSet()
	limitFlag := fs.String("limit", "", "intended limit, such as 100/60s")
	methodFlag := fs.String("method", "", "HTTP method")
	bodyFlag := fs.String("body", "", "request body")
	maxFlag := fs.Int("max-requests", DefaultMaxRequests, "hard cap on planned requests")
	yesFlag := fs.Bool("yes", false, "allow a host that is not loopback")
	dryFlag := fs.Bool("dry-run", false, "print the plan and send nothing")
	jsonFlag := fs.Bool("json", false, "print the report as JSON")
	configFlag := fs.String("config", "", "path to a YAML config")
	selfTest := fs.Bool("self-test", false, "run the built-in broken limiters")
	var headers, identities, origins, checks []string
	fs.Func("header", "header sent on every request (repeatable)", func(s string) error {
		headers = append(headers, s)
		return nil
	})
	fs.Func("identity", "one client, as headers (repeatable)", func(s string) error {
		identities = append(identities, s)
		return nil
	})
	fs.Func("origin", "extra host (repeatable)", func(s string) error {
		origins = append(origins, s)
		return nil
	})
	fs.Func("check", "check name (repeatable)", func(s string) error {
		checks = append(checks, s)
		return nil
	})
	if err := fs.Parse(reorderArgs(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return Command{}, ErrHelp
		}
		return Command{}, err
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flagSetter) { visited[f.name] = true })

	if *selfTest {
		if fs.NArg() != 0 {
			return Command{}, fmt.Errorf("--self-test does not take a url")
		}
		return Command{Action: ActionSelfTest, Campaign: Campaign{JSON: *jsonFlag, MaxRequests: DefaultMaxRequests, Yes: true}}, nil
	}

	var file *fileConfig
	if *configFlag != "" {
		f, err := loadConfig(*configFlag)
		if err != nil {
			return Command{}, err
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
		return Command{}, fmt.Errorf("pass one url")
	}
	if rawURL == "" {
		return Command{}, fmt.Errorf("pass a url or --config")
	}
	targetURL, err := parseHTTPURL(rawURL)
	if err != nil {
		return Command{}, err
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
		return Command{}, fmt.Errorf("pass --limit, for example 100/60s")
	}
	limit, err := ParseLimit(limitRaw)
	if err != nil {
		return Command{}, err
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
		return Command{}, fmt.Errorf("method is empty")
	}

	body := ""
	if file != nil {
		body = file.Body
	}
	if visited["body"] {
		body = *bodyFlag
	}

	var baseHeaders []Header
	if file != nil {
		baseHeaders, err = headersFromMap(file.Headers)
		if err != nil {
			return Command{}, err
		}
	}
	if len(headers) > 0 {
		baseHeaders, err = parseHeaderFlags(headers)
		if err != nil {
			return Command{}, err
		}
	}

	ids := []Identity{}
	if file != nil {
		for i, raw := range file.Identities {
			h, err := headersFromMap(raw.Headers)
			if err != nil {
				return Command{}, err
			}
			name := strings.TrimSpace(raw.Name)
			if name == "" {
				name = fmt.Sprintf("client-%d", i+1)
			}
			ids = append(ids, Identity{Name: name, Headers: h})
		}
	}
	if len(identities) > 0 {
		ids, err = parseIdentityFlags(identities)
		if err != nil {
			return Command{}, err
		}
	}
	if len(ids) == 0 {
		ids = []Identity{{Name: "default"}}
	}

	var originURLs []*url.URL
	originURLs = append(originURLs, targetURL)
	rawOrigins := []string{}
	if file != nil && len(origins) == 0 {
		rawOrigins = file.Origins
	}
	if len(origins) > 0 {
		rawOrigins = origins
	}
	if len(rawOrigins) > 0 {
		originURLs = nil
		for _, raw := range rawOrigins {
			u, err := parseHTTPURL(raw)
			if err != nil {
				return Command{}, fmt.Errorf("origin %q: %w", raw, err)
			}
			originURLs = append(originURLs, u)
		}
	}
	parsedOrigins := make([]Origin, 0, len(originURLs))
	seenOrigin := map[string]bool{}
	for _, u := range originURLs {
		key := u.Scheme + "://" + u.Host
		if seenOrigin[key] {
			continue
		}
		seenOrigin[key] = true
		parsedOrigins = append(parsedOrigins, Origin{Scheme: u.Scheme, Host: u.Host})
	}

	var kindNames []string
	if file != nil && len(checks) == 0 {
		kindNames = file.Checks
	}
	if len(checks) > 0 {
		for _, c := range checks {
			kindNames = append(kindNames, strings.Split(c, ",")...)
		}
	}
	kinds, err := parseChecks(kindNames, len(parsedOrigins))
	if err != nil {
		return Command{}, err
	}

	maxRequests := DefaultMaxRequests
	if file != nil && file.MaxRequests != nil {
		maxRequests = *file.MaxRequests
	}
	if visited["max-requests"] {
		maxRequests = *maxFlag
	}
	if maxRequests < 1 {
		return Command{}, fmt.Errorf("--max-requests must be at least 1")
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

	return Command{
		Action: ActionRun,
		Campaign: Campaign{
			Target: Target{
				URL:     targetURL,
				Method:  method,
				Body:    body,
				Headers: baseHeaders,
			},
			Limit:       limit,
			Identities:  ids,
			Origins:     parsedOrigins,
			Checks:      kinds,
			MaxRequests: maxRequests,
			Yes:         yes,
			DryRun:      dry,
			JSON:        jsonOut,
		},
	}, nil
}

// ParseLimit reads forms such as 100/60s and 5/500ms.
func ParseLimit(raw string) (Limit, error) {
	s := strings.TrimSpace(raw)
	countRaw, windowRaw, ok := strings.Cut(s, "/")
	if !ok {
		return Limit{}, fmt.Errorf("limit %q must look like 100/60s", raw)
	}
	countRaw = strings.TrimSpace(countRaw)
	windowRaw = strings.TrimSpace(windowRaw)
	n, err := strconv.Atoi(countRaw)
	if err != nil || n < 1 {
		return Limit{}, fmt.Errorf("limit count in %q must be a positive integer", raw)
	}
	w, err := time.ParseDuration(windowRaw)
	if err != nil || w <= 0 {
		return Limit{}, fmt.Errorf("limit window in %q must be a positive duration", raw)
	}
	return Limit{Count: n, Window: w}, nil
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("url %q must include a host", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url %q must be http or https", raw)
	}
	return u, nil
}

func parseChecks(names []string, origins int) ([]CheckKind, error) {
	if len(names) == 0 {
		out := append([]CheckKind{}, DefaultChecks...)
		if origins >= 2 {
			out = append(out, CheckMultiInstance)
		}
		return out, nil
	}
	seen := map[CheckKind]bool{}
	var out []CheckKind
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		kind := CheckKind(name)
		if !knownCheck(kind) {
			return nil, fmt.Errorf("unknown check %q", name)
		}
		if seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no checks selected")
	}
	return out, nil
}

func knownCheck(k CheckKind) bool {
	switch k {
	case CheckAccuracy, CheckBoundary, CheckBurst, CheckIsolation, CheckHeaders, CheckRecovery, CheckMultiInstance:
		return true
	default:
		return false
	}
}

func parseIdentityFlags(raws []string) ([]Identity, error) {
	out := make([]Identity, 0, len(raws))
	for i, raw := range raws {
		name, headers, err := splitIdentity(raw)
		if err != nil {
			return nil, fmt.Errorf("identity %q: %w", raw, err)
		}
		if name == "" {
			name = fmt.Sprintf("client-%d", i+1)
		}
		out = append(out, Identity{Name: name, Headers: headers})
	}
	return out, nil
}

func splitIdentity(raw string) (string, []Header, error) {
	parts := splitHeaderParts(raw)
	name := ""
	var headers []Header
	for _, part := range parts {
		if strings.HasPrefix(part, "name=") && name == "" && len(headers) == 0 {
			name = strings.TrimSpace(strings.TrimPrefix(part, "name="))
			continue
		}
		h, err := parseOneHeader(part)
		if err != nil {
			return "", nil, err
		}
		headers = append(headers, h)
	}
	if len(headers) == 0 {
		return "", nil, fmt.Errorf("needs a header")
	}
	return name, headers, nil
}

func parseHeaderFlags(raws []string) ([]Header, error) {
	var out []Header
	for _, raw := range raws {
		for _, part := range splitHeaderParts(raw) {
			h, err := parseOneHeader(part)
			if err != nil {
				return nil, fmt.Errorf("header %q: %w", raw, err)
			}
			out = append(out, h)
		}
	}
	return out, nil
}

func splitHeaderParts(raw string) []string {
	chunks := strings.Split(raw, ";")
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		c = strings.TrimSpace(c)
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

func parseOneHeader(raw string) (Header, error) {
	name, value, ok := strings.Cut(raw, ":")
	if !ok {
		return Header{}, fmt.Errorf("%q needs a colon between name and value", raw)
	}
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" {
		return Header{}, fmt.Errorf("header name is empty")
	}
	return Header{Name: name, Value: value}, nil
}

func headersFromMap(m map[string]string) ([]Header, error) {
	if len(m) == 0 {
		return nil, nil
	}
	// YAML maps do not keep order. Sort by name so a repeat run sends the same plan.
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sortStrings(names)
	out := make([]Header, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header name is empty")
		}
		out = append(out, Header{Name: name, Value: m[name]})
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
}

type fileIdentity struct {
	Name    string            `yaml:"name"`
	Headers map[string]string `yaml:"headers"`
}

func loadConfig(path string) (*fileConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
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
