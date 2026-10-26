package codegen

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/builtin"
	"artemis/pkg/dsl/lower"
)

// Every builtin the language has needs a spelling in both targets, or `artemis
// build` emits a call to something that does not exist -- or, as this package's
// own arity table once did, refuses a call `artemis run` accepts. The names and
// bounds are pkg/dsl/builtin's; what is tested here is that each target's switch
// has a case for every one of them, by emitting a call of each.
func TestEveryBuiltinHasASpelling(t *testing.T) {
	for _, name := range builtin.Names() {
		fn, _ := builtin.Lookup(name)
		stepType := "api"
		if fn.Scope == builtin.Page {
			stepType = "browser"
		}
		// A string satisfies every parameter, match()'s pattern included.
		args := make([]string, fn.Max())
		for i := range args {
			args[i] = `"x"`
		}
		expression := name + "(" + strings.Join(args, ", ") + `) == "x"`
		scenarios, err := lower.File(parse(t, "builtin.art", scenarioAround(stepType, expression)), nil)
		if err != nil {
			t.Fatalf("%s: lower: %v", expression, err)
		}
		value := scenarios[0].Steps[0].Expects[0].Value

		py := &pyFile{used: map[string]bool{}, mods: map[string]bool{}, taken: map[string]bool{}, step: stepType}
		if _, err := py.expr(value, precOr); err != nil {
			t.Errorf("python: %s: %v", expression, err)
		}
		js := &jsFile{used: map[string]bool{}, imports: map[string]bool{}, step: stepType}
		if _, err := js.expr(value, jsPrecOr); err != nil {
			t.Errorf("javascript: %s: %v", expression, err)
		}
	}
}

// arity() reads pkg/dsl/builtin's bounds the way both emitters ask for them:
// every count in a builtin's range emits, the count past its maximum does not,
// and the bounds it answers are the ones its error message prints.
func TestArityReadsTheTable(t *testing.T) {
	for _, name := range builtin.Names() {
		low, high, _ := builtin.Arity(name)
		if _, gotLow, gotHigh := arity(name, low); gotLow != low || gotHigh != high {
			t.Errorf("%s() takes %d to %d arguments here and %d to %d in pkg/dsl/builtin",
				name, gotLow, gotHigh, low, high)
		}
		for n := low; n <= high; n++ {
			if ok, _, _ := arity(name, n); !ok {
				t.Errorf("%s() with %d argument(s) is rejected here and accepted by the checker", name, n)
			}
		}
		if ok, _, _ := arity(name, high+1); ok {
			t.Errorf("%s() with %d argument(s) is accepted here and rejected by the checker", name, high+1)
		}
	}
	if ok, _, _ := arity("nope", 1); ok {
		t.Error("arity() accepted a name that is not a builtin")
	}
}
