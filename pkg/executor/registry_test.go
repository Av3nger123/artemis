package executor

import (
	"reflect"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

func noop() Executor {
	return Func(func(_ models.Step, _ Scope) (*result.StepResult, error) {
		return &result.StepResult{}, nil
	})
}

func TestRegistryLookupFindsWhatWasRegistered(t *testing.T) {
	reg := NewRegistry()
	http, exec := &recorder{}, &recorder{}
	reg.Register("http", http)
	reg.Register("exec", exec)

	got, ok := reg.Lookup("http")
	if !ok {
		t.Fatal("Lookup(\"http\") found nothing")
	}
	if got != Executor(http) {
		t.Error("Lookup(\"http\") returned the wrong executor")
	}
	if got, _ := reg.Lookup("exec"); got != Executor(exec) {
		t.Error("Lookup(\"exec\") returned the wrong executor")
	}
}

func TestRegistryLookupMissesAnUnregisteredType(t *testing.T) {
	reg := NewRegistry()
	reg.Register("http", noop())

	if _, ok := reg.Lookup("db"); ok {
		t.Error("Lookup(\"db\") found something in a registry that has only http")
	}
}

// Exact match, so the spellings that work stay the spellings that are
// documented.
func TestRegistryLookupIsCaseSensitive(t *testing.T) {
	reg := NewRegistry()
	reg.Register("http", noop())

	for _, near := range []string{"HTTP", "Http", " http", "http "} {
		if _, ok := reg.Lookup(near); ok {
			t.Errorf("Lookup(%q) matched; the match is meant to be exact", near)
		}
	}
}

func TestRegistryTypesIsSorted(t *testing.T) {
	reg := NewRegistry()
	for _, stepType := range []string{"http", "db", "exec", "browser"} {
		reg.Register(stepType, noop())
	}

	want := []string{"browser", "db", "exec", "http"}
	if got := reg.Types(); !reflect.DeepEqual(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
}

func TestEmptyRegistryHasNoTypes(t *testing.T) {
	if got := NewRegistry().Types(); len(got) != 0 {
		t.Errorf("Types() = %v, want nothing", got)
	}
}

func TestRegistryPanicsOnMisregistration(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Registry)
	}{
		{"duplicate type", func(r *Registry) {
			r.Register("http", noop())
			r.Register("http", noop())
		}},
		{"empty type", func(r *Registry) { r.Register("", noop()) }},
		{"nil executor", func(r *Registry) { r.Register("http", nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Register did not panic on a %s", tt.name)
				}
			}()
			tt.run(NewRegistry())
		})
	}
}

// A zero Registry is not how one is meant to be made, but it must not be the
// one way to get a nil-map write panic out of Register.
func TestZeroRegistryIsUsable(t *testing.T) {
	var reg Registry
	reg.Register("http", noop())

	if _, ok := reg.Lookup("http"); !ok {
		t.Error("Lookup(\"http\") found nothing after Register on a zero registry")
	}
}

func TestNilRegistryReads(t *testing.T) {
	var reg *Registry
	if _, ok := reg.Lookup("http"); ok {
		t.Error("Lookup on a nil registry found something")
	}
	if got := reg.Types(); len(got) != 0 {
		t.Errorf("Types() on a nil registry = %v, want nothing", got)
	}
}

// The default registry is the binary's; nothing is registered into it yet, and
// the first thing to do so is ART-16.
func TestDefaultRegistryIsTheSameRegistryEveryTime(t *testing.T) {
	first, second := Default(), Default()
	if first == nil {
		t.Fatal("Default() = nil")
	}
	if first != second {
		t.Error("Default() returned two different registries")
	}
}

// The package-level Register writes into the default registry. The swap keeps
// this test from leaving a step type behind for every other test to see.
func TestPackageRegisterWritesIntoTheDefaultRegistry(t *testing.T) {
	saved := defaultRegistry
	defaultRegistry = NewRegistry()
	defer func() { defaultRegistry = saved }()

	Register("http", noop())
	if _, ok := Default().Lookup("http"); !ok {
		t.Error("Register did not put the executor in the default registry")
	}
}
