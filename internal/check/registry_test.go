package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/perkinsjr/limitcheck/internal/spec"
)

func TestRegistryListsEveryCheckConstructor(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var ctors []string
	var registry string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == "registry.go" {
			registry = string(src)
		}
		file, err := parser.ParseFile(fset, e.Name(), src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			if fn.Type.Params != nil && len(fn.Type.Params.List) != 0 {
				continue
			}
			id, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
			if !ok || id.Name != "Check" || !strings.HasSuffix(fn.Name.Name, "Check") {
				continue
			}
			ctors = append(ctors, fn.Name.Name)
		}
	}
	if len(ctors) == 0 {
		t.Fatal("no check constructors")
	}
	for _, name := range ctors {
		if !strings.Contains(registry, name+"(") {
			t.Errorf("%s is not listed in All", name)
		}
	}
}

func TestSelectedDefaults(t *testing.T) {
	in := spec.Input{Policy: spec.Policy{Rate: spec.Rate{Count: 5}}}
	got, err := Selected(in)
	if err != nil {
		t.Fatal(err)
	}
	names := namesOf(got)
	if strings.Join(names, ",") != "headers,accuracy,boundary,burst,recovery" {
		t.Fatal(names)
	}
	in.Identities = []spec.Identity{{Name: "a"}, {Name: "b"}}
	in.Origins = []spec.Origin{{Host: "a"}, {Host: "b"}}
	in.Sharing = true
	got, err = Selected(in)
	if err != nil {
		t.Fatal(err)
	}
	names = namesOf(got)
	if strings.Join(names, ",") != "headers,accuracy,boundary,burst,recovery,isolation,multi_instance" {
		t.Fatal(names)
	}
	in.Checks = []string{"burst", "multi-instance"}
	got, err = Selected(in)
	if err != nil {
		t.Fatal(err)
	}
	names = namesOf(got)
	if strings.Join(names, ",") != "burst,multi_instance" {
		t.Fatal(names)
	}
}

func namesOf(checks []Check) []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = c.Name
	}
	return out
}
