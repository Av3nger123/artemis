package lower

import (
	"errors"
	"testing"

	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
	"artemis/pkg/eval"
)

// A step a use brought in was written in the collection's file, so its line
// -- and its expects' and captures' lines -- are lines of that file, and say
// so. A step written in the scenario names no file: it would only repeat the
// scenario's.
func TestAnExpandedStepNamesTheFileItWasWrittenIn(t *testing.T) {
	files := expand.MapLoader{
		"c.art":    "collection \"c\" {\n  request r() {\n    get \"x\"\n    expect status == 200\n    capture tok = body.t\n  }\n}\n",
		"main.art": "import \"c.art\"\n\nscenario \"s\" {\n  use c.r\n  step \"own\" {\n    get \"y\"\n    expect status == 200\n  }\n}\n",
	}
	u := front.CompileWith("main.art", files["main.art"], files)
	if u.Bag.HasErrors() {
		t.Fatal(u.Bag.All())
	}
	scs, err := File(u.Expanded, u.Info)
	if err != nil {
		t.Fatal(err)
	}
	used, own := scs[0].Steps[0], scs[0].Steps[1]
	if used.File != "c.art" || used.Line != 2 {
		t.Errorf("used step at %q:%d, want c.art:2", used.File, used.Line)
	}
	if e := used.Expects[0]; e.File != "c.art" || e.Line != 4 {
		t.Errorf("expect at %q:%d, want c.art:4", e.File, e.Line)
	}
	if c := used.Captures[0]; c.File != "c.art" || c.Line != 5 {
		t.Errorf("capture at %q:%d, want c.art:5", c.File, c.Line)
	}
	if own.File != "" || own.Expects[0].File != "" {
		t.Errorf("a step written in the scenario names a file: %q, %q", own.File, own.Expects[0].File)
	}

	a := used.Expects[0].Assert(used.Name, &eval.Env{Roots: map[string]any{"status": float64(500)}})
	if a.File != "c.art" || a.Line != 4 {
		t.Errorf("assertion at %q:%d, want c.art:4", a.File, a.Line)
	}
	if got := used.Captures[0].errored(used.Name, errors.New("x")); got.File != "c.art" {
		t.Errorf("errored capture names %q, want c.art", got.File)
	}
}
