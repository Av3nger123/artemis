package executor

import (
	"reflect"
	"testing"
)

func TestNewScopeIsEmptyAndWritable(t *testing.T) {
	s := NewScope()
	if len(s) != 0 {
		t.Fatalf("NewScope() has %d keys, want 0", len(s))
	}
	s.Set("token", "abc")
	if got, ok := s.Get("token"); !ok || got != "abc" {
		t.Errorf("Get(%q) = %v, %v; want \"abc\", true", "token", got, ok)
	}
}

// A capture keeps the type the value had: ART-6 renders a non-string, and an id
// that arrives as a float64 must not be flattened to a string on the way in.
func TestScopeKeepsValueTypes(t *testing.T) {
	s := NewScope()
	s.Set("id", float64(42))
	s.Set("ok", true)

	if got, _ := s.Get("id"); got != float64(42) {
		t.Errorf("Get(\"id\") = %#v, want float64(42)", got)
	}
	if got, _ := s.Get("ok"); got != true {
		t.Errorf("Get(\"ok\") = %#v, want true", got)
	}
}

func TestScopeSetReplaces(t *testing.T) {
	s := NewScope()
	s.Set("token", "first")
	s.Set("token", "second")

	if got, _ := s.Get("token"); got != "second" {
		t.Errorf("Get(\"token\") = %v, want \"second\"", got)
	}
}

// "Set to nil" and "never set" are different answers, and a capture of a JSON
// null is the first one.
func TestScopeGetDistinguishesNilFromAbsent(t *testing.T) {
	s := NewScope()
	s.Set("present", nil)

	if got, ok := s.Get("present"); got != nil || !ok {
		t.Errorf("Get(\"present\") = %v, %v; want nil, true", got, ok)
	}
	if got, ok := s.Get("absent"); got != nil || ok {
		t.Errorf("Get(\"absent\") = %v, %v; want nil, false", got, ok)
	}
}

func TestScopeOfAdoptsTheMap(t *testing.T) {
	vars := map[string]any{"base": "http://localhost"}
	s := ScopeOf(vars)

	s.Set("token", "abc")
	if vars["token"] != "abc" {
		t.Error("ScopeOf copied the map; it is meant to adopt it")
	}
	if got, _ := s.Get("base"); got != "http://localhost" {
		t.Errorf("Get(\"base\") = %v, want the value it was made with", got)
	}
}

func TestScopeOfNilIsUsable(t *testing.T) {
	s := ScopeOf(nil)
	s.Set("k", 1)
	if got, _ := s.Get("k"); got != 1 {
		t.Errorf("Get(\"k\") = %v, want 1", got)
	}
}

// Vars is what pkg/shared's templating is handed, so it must be the scope
// itself: a copy would mean a step's captures were invisible to the next step.
func TestVarsIsTheScopeItself(t *testing.T) {
	s := NewScope()
	s.Set("token", "abc")

	vars := s.Vars()
	if !reflect.DeepEqual(vars, map[string]any{"token": "abc"}) {
		t.Fatalf("Vars() = %#v, want the scope's contents", vars)
	}
	s.Set("id", 1)
	if _, ok := vars["id"]; !ok {
		t.Error("Vars() returned a copy; it is meant to be the scope itself")
	}
}

// A nil scope is something a zero-valued caller can still read from without a
// nil check at every use.
func TestNilScopeReads(t *testing.T) {
	var s Scope
	if _, ok := s.Get("anything"); ok {
		t.Error("Get on a nil scope found something")
	}
	if got := s.Vars(); got == nil || len(got) != 0 {
		t.Errorf("Vars() on a nil scope = %#v, want an empty map", got)
	}
}
