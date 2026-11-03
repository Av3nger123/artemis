package parser

import (
	"os"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
)

// TestFlowFileParsesWithTheFullSyntax is the plan's flow file, which also
// needs private and value-less vars, a var after a step's action, and the
// step form of a use. It runs once those land beside this.
func TestFlowFileParsesWithTheFullSyntax(t *testing.T) {
	src := "import \"../collections/auth.art\"\n\nflow checkout {\n  var code = 200\n  private var url = env(\"U\")\n" +
		"  step \"log in\" = auth.login { }\n  step \"pay\" {\n    post \"${url}/pay\"\n    expect status == code\n    var pay_status = status\n  }\n" +
		"  if pay_status == 400 {\n    step \"err\" {\n      get \"/e\"\n    }\n  }\n}\n"
	f := mustParse(t, src)
	fl, ok := f.Scenarios[1].(*ast.Flow)
	if !ok || fl.Name.Value != "checkout" {
		t.Fatalf("Scenarios[1] = %T, want the flow checkout", f.Scenarios[1])
	}
}

func TestFlowFileParses(t *testing.T) {
	src := "import \"../collections/auth.art\"\n\nflow checkout {\n  var code = 200\n" +
		"  use auth.login\n  step \"pay\" {\n    post \"/pay\"\n    expect status == code\n    capture pay_status = status\n  }\n" +
		"  if pay_status == 400 {\n    step \"err\" {\n      get \"/e\"\n    }\n  }\n}\n"
	f := mustParse(t, src)
	fl, ok := f.Scenarios[1].(*ast.Flow)
	if !ok || fl.Name.Value != "checkout" {
		t.Fatalf("Scenarios[1] = %T, want the flow checkout", f.Scenarios[1])
	}
	if len(fl.Body) != 4 {
		t.Fatalf("flow body has %d decls, want var, use, step, if", len(fl.Body))
	}
}

func TestScenarioFileParses(t *testing.T) {
	src := "flow \"../flows/checkout.art\"\n\nscenario \"full price\" { }\n" +
		"scenario \"bad coupon\" {\n  tags = [\"errors\"]\n  coupon = \"NOPE\"\n  code = 422\n  total = skip\n}\n" +
		"scenario \"one line\" { quantity = 3  coupon = \"TEN\"  total = 270 }\n"
	f := mustParse(t, src)
	ref, ok := f.Scenarios[0].(*ast.FlowRef)
	if !ok || ref.Path.Value != "../flows/checkout.art" {
		t.Fatalf("Scenarios[0] = %T, want the header", f.Scenarios[0])
	}
	body := f.Scenarios[2].(*ast.Scenario).Body
	if len(body) != 4 {
		t.Fatalf("bad coupon holds %d lines, want tags and three sets", len(body))
	}
	if _, ok := body[0].(*ast.TagsDecl); !ok {
		t.Fatalf("body[0] = %T, want tags", body[0])
	}
	for _, d := range body[1:] {
		if _, ok := d.(*ast.Set); !ok {
			t.Fatalf("%T in a data scenario, want a set", d)
		}
	}
	if last := body[3].(*ast.Set); !last.IsSkip() || last.Name.Value != "total" {
		t.Fatalf("the last set is %q = %s, want total = skip", last.Name.Value, ast.Source(last.Value))
	}
	if n := len(f.Scenarios[3].(*ast.Scenario).Body); n != 3 {
		t.Fatalf("one line holds %d sets, want 3", n)
	}
}

func TestDataScenarioRejectsSteps(t *testing.T) {
	src := "flow \"../flows/checkout.art\"\n\nscenario \"x\" {\n  step \"s\" {\n    get \"/\"\n  }\n  code = 1\n}\n"
	f, bag := Parse("t.art", src)
	assertRoundTrip(t, src, f)
	all := bag.All()
	if len(all) != 1 || all[0].Code != diag.DataOnlyScenario || all[0].Span.Line != 4 || all[0].Span.Col != 3 {
		t.Fatalf("diagnostics = %v, want one data-only-scenario at step", all)
	}
	if all[0].Hint != "put steps in the flow, ../flows/checkout.art" {
		t.Fatalf("hint = %q", all[0].Hint)
	}
	body := f.Scenarios[1].(*ast.Scenario).Body
	if _, ok := body[len(body)-1].(*ast.Set); !ok {
		t.Fatalf("the set after the step was lost to recovery: %T", body[len(body)-1])
	}
}

func TestDataScenarioFaults(t *testing.T) {
	head := "flow \"f.art\"\n"
	for _, tc := range []struct {
		name, src string
		want      diag.Code
	}{
		{"var", head + "scenario \"x\" {\n  var a = 1\n}\n", diag.DataOnlyScenario},
		{"expect", head + "scenario \"x\" {\n  expect status == 200\n}\n", diag.DataOnlyScenario},
		{"use", head + "scenario \"x\" {\n  use auth.login\n}\n", diag.DataOnlyScenario},
		{"block", head + "scenario \"x\" {\n  if a {\n    b = 1\n  }\n}\n", diag.DataOnlyScenario},
		{"missing =", head + "scenario \"x\" {\n  coupon \"TEN\"\n}\n", diag.UnexpectedToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, bag := Parse("t.art", tc.src)
			assertRoundTrip(t, tc.src, f)
			all := bag.All()
			if len(all) != 1 || all[0].Code != tc.want {
				t.Fatalf("diagnostics = %v, want one %s", all, tc.want)
			}
		})
	}
}

func TestScenarioWithoutHeaderIsStillTheOldForm(t *testing.T) {
	f := mustParse(t, "scenario \"x\" {\n  step \"s\" {\n    get \"/\"\n  }\n}\n")
	if _, ok := f.Scenarios[0].(*ast.Scenario).Body[0].(*ast.StepDecl); !ok {
		t.Fatal("a scenario with no header lost its step")
	}
}

func TestFlowFileFaults(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []diag.Code
	}{
		{"header below an import", "import \"a.art\"\nflow \"f.art\"\n", []diag.Code{diag.FlowRefPlacement}},
		{"two headers", "flow \"f.art\"\nflow \"g.art\"\n", []diag.Code{diag.SecondFlow}},
		{"two flows", "flow a {\n}\nflow b {\n}\n", []diag.Code{diag.SecondFlow}},
		{"a flow in a scenario file", "flow \"f.art\"\nflow b {\n}\n", []diag.Code{diag.SecondFlow}},
		{"a header in a flow file", "flow a {\n}\nflow \"f.art\"\n", []diag.Code{diag.SecondFlow}},
		{"a collection and a scenario in a flow file",
			"collection \"c\" {\n}\nflow a {\n}\nscenario \"s\" {\n}\n",
			[]diag.Code{diag.FlowFileHolds, diag.FlowFileHolds}},
		{"flow with nothing after it", "flow\n", []diag.Code{diag.UnexpectedToken}},
		{"an unclosed flow", "flow a {\n", []diag.Code{diag.UnclosedBlock}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, bag := Parse("t.art", tc.src)
			assertRoundTrip(t, tc.src, f)
			var got []diag.Code
			for _, d := range bag.All() {
				got = append(got, d.Code)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("codes = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("codes = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestFlowIsStillAName holds the positional-keyword rule: flow is a keyword
// only at the top level of a file, and a var or a capture may be called flow.
func TestFlowIsStillAName(t *testing.T) {
	mustParse(t, "scenario \"s\" {\n  var flow = 1\n  step \"a\" {\n    get \"/\"\n    capture flow2 = flow\n  }\n}\n")
}

func TestFlowFixturesParseClean(t *testing.T) {
	for _, path := range []string{"testdata/flowfile.art", "testdata/scenariofile.art"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mustParse(t, string(src))
	}
}
