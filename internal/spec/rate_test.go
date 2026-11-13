package spec

import (
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	r, err := ParseRate("100/60s")
	if err != nil {
		t.Fatal(err)
	}
	if r.Count != 100 || r.Window != 60*time.Second {
		t.Fatalf("%+v", r)
	}
	r, err = ParseRate("5 / 500ms")
	if err != nil {
		t.Fatal(err)
	}
	if r.Count != 5 || r.Window != 500*time.Millisecond {
		t.Fatalf("%+v", r)
	}
	if _, err := ParseRate("0/1s"); err == nil {
		t.Fatal("zero count")
	}
	if _, err := ParseRate("nope"); err == nil {
		t.Fatal("missing slash")
	}
}

func TestParseIdentity(t *testing.T) {
	id, err := ParseIdentity("name=alice; X-Identity: a")
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "alice" || len(id.Headers) != 1 || id.Headers[0].Value != "a" {
		t.Fatalf("%+v", id)
	}
}
