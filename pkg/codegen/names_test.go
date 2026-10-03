package codegen

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
)

func TestLocalRenamesOnlyWhatItHasTo(t *testing.T) {
	for name, want := range map[string]string{
		"token":      "token",
		"order_id":   "order_id",
		"class":      "class_", // a python keyword
		"None":       "None_",
		"match":      "match_",  // a soft keyword, and a name a .art file uses
		"status":     "status_", // a root of the api step
		"exit_code":  "exit_code_",
		"page":       "page_",
		"resp":       "resp_", // a local the api step binds
		"requests":   "requests_",
		"int":        "int_",
		"art_render": "art_render_", // a helper
	} {
		if got := local(name); got != want {
			t.Errorf("local(%q) = %q, want %q", name, got, want)
		}
	}
}

// Every root of every step type has to be renamed around, because one test
// function holds every step of its scenario: a var called `status` would be
// clobbered by the api step that binds the root.
func TestLocalRenamesAroundEveryRootOfEveryStepType(t *testing.T) {
	for _, typ := range check.StepTypes() {
		for _, root := range check.Roots(typ) {
			if local(root) == root {
				t.Errorf("local(%q) = %q; %s binds that root", root, root, typ)
			}
		}
	}
}

// reference is eval.Env.lookup: roots before vars, per step. This is the one rule
// that decides what `status` means, and it means two different things in one
// scenario.
func TestReferenceResolvesRootsBeforeBindings(t *testing.T) {
	api, _ := lower.TypeKey(check.API)
	terminal, _ := lower.TypeKey(check.Terminal)
	browser, _ := lower.TypeKey(check.Browser)

	for _, c := range []struct {
		name, step, want string
	}{
		{"status", api, "status"},       // the root
		{"status", terminal, "status_"}, // the var, renamed
		{"status", "", "status_"},       // a var's own value: no roots at all
		{"exit_code", terminal, "exit_code"},
		{"exit_code", api, "exit_code_"},
		{"page", browser, "page"},
		{"page", api, "page_"},
		{"token", api, "token"},
		{"class", browser, "class_"},
	} {
		if got := reference(c.name, c.step); got != c.want {
			t.Errorf("reference(%q, %q) = %q, want %q", c.name, c.step, got, c.want)
		}
	}
}

func TestTestNameAndFileName(t *testing.T) {
	for name, want := range map[string]string{
		"checkout":           "test_checkout",
		"the order is there": "test_the_order_is_there",
		"sign-in":            "test_sign_in",
		"2FA":                "test_2fa",
		"  ":                 "test_scenario",
		"":                   "test_scenario",
	} {
		if got := testName(name); got != want {
			t.Errorf("testName(%q) = %q, want %q", name, got, want)
		}
	}

	for path, want := range map[string]string{
		"checkout.art":            "test_checkout.py",
		"suites/my-suite.art":     "test_my_suite.py",
		"/abs/path/02_orders.art": "test_02_orders.py",
		".art":                    "test_artemis.py",
	} {
		if got := fileName(path); got != want {
			t.Errorf("fileName(%q) = %q, want %q", path, got, want)
		}
	}
}

// A generated test name is what pytest collects, so two scenarios that slug alike
// must not become one function -- the second would silently replace the first and
// the suite would quietly run half of what it says it does.
func TestUniqueKeepsAlikeNamesApart(t *testing.T) {
	taken := map[string]bool{}
	got := []string{
		unique(testName("sign in"), taken),
		unique(testName("sign-in"), taken),
		unique(testName("sign  in"), taken),
	}
	want := []string{"test_sign_in", "test_sign_in_2", "test_sign_in_3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Every name the emitter puts in a test function's scope has to be in the set
// local() renames around, or a capture with that name would shadow it. This
// checks the ones the generated body actually writes.
func TestEmittedNamesAreRenamedAround(t *testing.T) {
	for _, name := range []string{
		"resp", "proc", "attempt", "page", "browser", "int",
		"os", "re", "json", "time", "requests", "subprocess", "pytest", "expect",
	} {
		if local(name) == name {
			t.Errorf("local(%q) = %q; the generated file already uses that name", name, name)
		}
	}
}

// Every helper's Python name has the prefix, so local() renames around all of
// them without listing them.
func TestEveryHelperNameIsCoveredByThePrefixRule(t *testing.T) {
	for _, h := range helpers {
		if h.name == helperBrowser {
			continue // the fixture, named for what a test writes in its signature
		}
		if !strings.HasPrefix(h.name, helperPrefix) {
			t.Errorf("helper %q does not start with %q", h.name, helperPrefix)
		}
		if local(h.name) == h.name {
			t.Errorf("local(%q) = %q; a capture with that name would shadow the helper", h.name, h.name)
		}
	}
}
