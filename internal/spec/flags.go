package spec

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// errFlagHelp is the help signal from the standard flag package, renamed so callers
// can use errors.Is without importing flag.
var errFlagHelp = flag.ErrHelp

// flagSet is a small wrapper so Visit can see which names the user typed.
type flagSet struct {
	fs *flag.FlagSet
}

type flagSetter struct {
	name string
}

// valueFlags take the next argument when it is not attached with '='.
var valueFlags = map[string]bool{
	"limit":        true,
	"method":       true,
	"body":         true,
	"max-requests": true,
	"config":       true,
	"header":       true,
	"identity":     true,
	"origin":       true,
	"check":        true,
	"scenario":     true,
}

// reorderArgs moves positionals to the end so a url can sit before flags.
func reorderArgs(args []string) []string {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if valueFlags[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}

func newFlagSet() *flagSet {
	fs := flag.NewFlagSet("limitcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flagSet{fs: fs}
}

func (s *flagSet) String(name, value, usage string) *string {
	return s.fs.String(name, value, usage)
}

func (s *flagSet) Int(name string, value int, usage string) *int {
	return s.fs.Int(name, value, usage)
}

func (s *flagSet) Bool(name string, value bool, usage string) *bool {
	return s.fs.Bool(name, value, usage)
}

func (s *flagSet) Func(name, usage string, fn func(string) error) {
	s.fs.Func(name, usage, fn)
}

func (s *flagSet) Parse(args []string) error {
	return s.fs.Parse(args)
}

func (s *flagSet) NArg() int { return s.fs.NArg() }

func (s *flagSet) Arg(i int) string { return s.fs.Arg(i) }

func (s *flagSet) Visit(fn func(*flagSetter)) {
	s.fs.Visit(func(f *flag.Flag) {
		fn(&flagSetter{name: f.Name})
	})
}

func sortStrings(in []string) {
	sort.Strings(in)
}

// HasCheck reports whether the campaign selected kind.
func HasCheck(c Campaign, kind CheckKind) bool {
	for _, k := range c.Checks {
		if k == kind {
			return true
		}
	}
	return false
}

// RequestURL builds the URL for one origin, keeping the target path and query.
func RequestURL(c Campaign, origin int) (string, error) {
	if origin < 0 || origin >= len(c.Origins) {
		return "", fmt.Errorf("origin %d is out of range", origin)
	}
	if c.Target.URL == nil {
		return "", errors.New("target url is missing")
	}
	u := *c.Target.URL
	o := c.Origins[origin]
	u.Scheme = o.Scheme
	u.Host = o.Host
	return u.String(), nil
}
