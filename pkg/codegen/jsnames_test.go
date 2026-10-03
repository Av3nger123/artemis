package codegen

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
)

func TestJSLocalRenamesOnlyWhatItHasTo(t *testing.T) {
	for name, want := range map[string]string{
		"token":      "token",
		"order_id":   "order_id",
		"match":      "match", // not reserved in javascript, unlike python's soft keyword
		"int":        "int",   // not a javascript name at all
		"class":      "class_",
		"const":      "const_",
		"await":      "await_",
		"status":     "status_", // a root of the api step
		"exit_code":  "exit_code_",
		"page":       "page_",
		"resp":       "resp_", // a local the api step binds
		"test":       "test_", // what vitest's own import is called
		"expect":     "expect_",
		"process":    "process_",
		"art_render": "art_render_", // a helper
	} {
		if got := jsLocal(name); got != want {
			t.Errorf("jsLocal(%q) = %q, want %q", name, got, want)
		}
	}
}

// Every root of every step type has to be renamed around, because one test holds
// every step of its scenario: a var called `status` would be clobbered by the api
// step that binds the root.
func TestJSLocalRenamesAroundEveryRootOfEveryStepType(t *testing.T) {
	for _, typ := range check.StepTypes() {
		for _, root := range check.Roots(typ) {
			if jsLocal(root) == root {
				t.Errorf("jsLocal(%q) = %q; %s binds that root", root, root, typ)
			}
		}
	}
}

// jsReference is eval.Env.lookup: roots before vars, per step. This is the one
// rule that decides what `status` means, and it means two different things in one
// scenario.
func TestJSReferenceResolvesRootsBeforeBindings(t *testing.T) {
	for _, c := range []struct {
		name, step, want string
	}{
		{"status", apiKey, "status"},       // the root
		{"status", terminalKey, "status_"}, // the var, renamed
		{"status", "", "status_"},          // a var's own value: no roots at all
		{"exit_code", terminalKey, "exit_code"},
		{"exit_code", apiKey, "exit_code_"},
		{"page", browserKey, "page"},
		{"page", apiKey, "page_"},
		{"token", apiKey, "token"},
		{"class", browserKey, "class_"},
	} {
		if got := jsReference(c.name, c.step); got != c.want {
			t.Errorf("jsReference(%q, %q) = %q, want %q", c.name, c.step, got, c.want)
		}
	}
}

// Every name the emitter puts in a test's scope has to be in the set jsLocal()
// renames around, or a capture with that name would shadow it.
func TestJSEmittedNamesAreRenamedAround(t *testing.T) {
	for _, name := range []string{
		"resp", "proc", "page", "test", "expect", "onTestFinished", "chromium",
		"spawnSync", "fetch", "process", "JSON", "Headers", "AbortSignal", "URL",
		"RegExp", "Number", "Math", "performance",
	} {
		if jsLocal(name) == name {
			t.Errorf("jsLocal(%q) = %q; the generated file already uses that name", name, name)
		}
	}
}

// The file name is what vitest collects, so it is a contract and not a detail:
// `<stem>.test.js` matches vitest's default include with no configuration, the
// way `test_<stem>.py` matches pytest's default discovery.
func TestJSFileName(t *testing.T) {
	for path, want := range map[string]string{
		"checkout.art":            "checkout.test.js",
		"suites/my-suite.art":     "my-suite.test.js",
		"suites/my_suite.art":     "my-suite.test.js",
		"/abs/path/02_orders.art": "02-orders.test.js",
		".art":                    "artemis.test.js",
	} {
		if got := jsFileName(path); got != want {
			t.Errorf("jsFileName(%q) = %q, want %q", path, got, want)
		}
	}
}

// A scenario's name is a vitest test's name verbatim, so two scenarios that slug
// alike stay two tests -- there is nothing to keep apart, which is the one place
// this target needs less machinery than the Python one. The test asserts the
// absence: a slug anywhere in the emitted name would be a bug.
func TestJSTestNamesAreTheScenarioNames(t *testing.T) {
	tree := parse(t, "multi.art", `scenario "sign in" {
}

scenario "sign-in" {
}
`)
	files, err := JS{}.Generate(tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`test("sign in", async () => {});`, `test("sign-in", async () => {});`} {
		if !strings.Contains(files[0].Content, want) {
			t.Errorf("the module does not hold %s:\n%s", want, files[0].Content)
		}
	}
}

// The roots of a step type are declared in check.Roots' order, which is what
// makes the `let` line at the top of a test the same every run.
func TestJSRootListMatchesTheChecker(t *testing.T) {
	for _, typ := range check.StepTypes() {
		key, ok := lower.TypeKey(typ)
		if !ok {
			continue
		}
		want := check.Roots(typ)
		got := jsRootList[key]
		if len(got) != len(want) {
			t.Fatalf("jsRootList[%q] = %v, want %v", key, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("jsRootList[%q][%d] = %q, want %q", key, i, got[i], want[i])
			}
		}
	}
}
