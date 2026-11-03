package front_test

import (
	"slices"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

// codes is every diagnostic's code in u, in file order.
func codes(u *front.Unit) []diag.Code {
	var out []diag.Code
	for _, d := range u.Bag.All() {
		out = append(out, d.Code)
	}
	return out
}

// A flow file compiled on its own gets the check a scenario file's flow gets:
// each fault of the flow is reported, in the flow file, with no chain.
func TestAFlowFileIsCheckedOnItsOwn(t *testing.T) {
	cases := map[string]struct {
		src  string
		want diag.Code
	}{
		"a var below the first step": {"flow f {\n  step \"s\" {\n    get \"http://h/\"\n  }\n  var late = 1\n}\n", diag.VarAfterStep},
		"a var named tags":           {"flow f {\n  var tags = 1\n  step \"s\" {\n    get \"http://h/\"\n  }\n}\n", diag.ReservedTags},
		"two vars of one name":       {"flow f {\n  var a = 1\n  var a = 2\n  step \"s\" {\n    get \"http://h/${a}\"\n  }\n}\n", diag.DuplicateBinding},
		"an unknown name":            {"flow f {\n  step \"s\" {\n    get \"http://h/${nope}\"\n  }\n}\n", diag.UnknownIdentifier},
		"two parallel branches writing one name": {"flow f {\n  parallel {\n    step \"a\" {\n      get \"http://h/a\"\n      var id = body.id\n    }\n" +
			"    step \"b\" {\n      get \"http://h/b\"\n      var id = body.id\n    }\n  }\n}\n", diag.ParallelCaptureClash},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			u := front.CompileWith("flows/f.art", c.src, expand.MapLoader{})
			got := codes(u)
			if !slices.Equal(got, []diag.Code{c.want}) {
				t.Fatalf("want %s alone, got %v", c.want, u.Bag.All())
			}
			if d := u.Bag.All()[0]; d.Span.File != "flows/f.art" || len(d.UsedFrom) != 0 {
				t.Errorf("want it in the flow file with no chain, got %+v", d)
			}
			if !u.FlowFile || len(u.Expanded.Scenarios) != 0 {
				t.Errorf("a flow file expands to no scenario: FlowFile %v, %d scenarios", u.FlowFile, len(u.Expanded.Scenarios))
			}
		})
	}
}

// A required var is given a placeholder for the check, as a scenario file's
// flow is: it is not var-needs-value, and a read of it resolves.
func TestAFlowFilesRequiredVarIsAPlaceholder(t *testing.T) {
	src := "flow f {\n  var url\n  private var tries = 1\n  step \"s\" {\n    get \"${url}/x\"\n    expect status == 200\n  }\n}\n"
	u := front.CompileWith("flows/f.art", src, nil)
	if u.Bag.Len() != 0 {
		t.Fatalf("want no diagnostic, got %v", u.Bag.All())
	}
	// The tree as written is untouched: the placeholder is the check's own.
	if got := ast.Source(u.Tree); got != src {
		t.Fatalf("the check rewrote the file's tree:\n%s", got)
	}
}

// The checker's Info is about the flow file's own steps, at any depth, which
// is what `artemis ast` encodes.
func TestAFlowFilesInfoIsAboutItsOwnSteps(t *testing.T) {
	files := expand.MapLoader{
		"collections/auth.art": "collection \"auth\" {\n  request login {\n    var url\n    post \"${url}/token\"\n    var token = body.t\n  }\n}\n",
	}
	src := "import \"../collections/auth.art\"\n\nflow f {\n  var url = \"http://h\"\n  setup {\n    step \"log in\" = auth.login { }\n  }\n" +
		"  step \"s\" {\n    get \"${url}/x\"\n    expect status == 200\n  }\n  if true {\n    step \"t\" {\n      run \"true\"\n    }\n  }\n}\n"
	u := front.CompileWith("flows/f.art", src, files)
	if u.Bag.Len() != 0 {
		t.Fatalf("want no diagnostic, got %v", u.Bag.All())
	}
	var flow *ast.Flow
	for _, d := range u.Tree.Scenarios {
		if f, ok := d.(*ast.Flow); ok {
			flow = f
		}
	}
	var steps []*ast.StepDecl
	ast.Inspect(flow, func(n ast.Node) {
		if s, ok := n.(*ast.StepDecl); ok {
			steps = append(steps, s)
		}
	})
	if len(steps) != 2 {
		t.Fatalf("want the flow's two steps, got %d", len(steps))
	}
	if got := u.Info.StepType(steps[0]); got != check.API {
		t.Errorf("step s is %v, want api", got)
	}
	if got := u.Info.StepType(steps[1]); got != check.Terminal {
		t.Errorf("step t is %v, want terminal", got)
	}
	if scope := u.Info.Scope(steps[0]); !slices.Contains(scope, "url") || !slices.Contains(scope, "token") {
		t.Errorf("step s's scope is %v, want url and the item's token in it", scope)
	}
}

// The duplicate top var's hint is the var's own: the first declaration, by
// its line in the flow -- whether the flow file is compiled alone or through
// a scenario file, where the use line is not where the first one is, and as
// would not help.
func TestADuplicateFlowVarNamesTheFirstDeclaration(t *testing.T) {
	files := expand.MapLoader{
		"flows/f.art": "flow f {\n  var a = 1\n  var a = 2\n  step \"s\" {\n    get \"http://h/${a}\"\n  }\n}\n",
	}
	for _, u := range []*front.Unit{
		front.CompileWith("flows/f.art", files["flows/f.art"], files),
		front.CompileWith("scenarios/x.art", "flow \"../flows/f.art\"\n\nscenario \"s\" { }\n", files),
	} {
		ds := u.Bag.All()
		if len(ds) != 1 || ds[0].Code != diag.DuplicateBinding {
			t.Fatalf("want one duplicate-binding, got %v", ds)
		}
		if ds[0].Span.File != "flows/f.art" || ds[0].Span.Line != 3 || ds[0].Hint != "the var at line 2 binds it first" {
			t.Errorf("want it at flows/f.art:3 naming line 2, got %s:%d %q", ds[0].Span.File, ds[0].Span.Line, ds[0].Hint)
		}
	}
}

// Two input vars of one name in an item are one declaration written twice:
// duplicate-binding, at the second, naming the first by its line. Two writes
// of one name after the item's action are not: the last write wins.
func TestTwoInputsOfOneNameInAnItemAreADuplicate(t *testing.T) {
	for name, inputs := range map[string]string{
		"both required":  "    var user\n    var user\n",
		"one private":    "    var user\n    private var user = \"a\"\n",
		"both defaulted": "    var user = \"a\"\n    var user = \"b\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			coll := "collection \"auth\" {\n  request login {\n" + inputs + "    post \"http://h/${user}\"\n  }\n}\n"
			u := front.CompileWith("collections/auth.art", coll, nil)
			ds := u.Bag.All()
			if len(ds) != 1 || ds[0].Code != diag.DuplicateBinding || ds[0].Span.Line != 4 || ds[0].Hint != "the var at line 3 binds it first" {
				t.Fatalf("want one duplicate-binding at line 4 naming line 3, got %v", ds)
			}
		})
	}
	coll := "collection \"auth\" {\n  request login {\n    var user\n    post \"http://h/${user}\"\n" +
		"    var token = body.a\n    var token = body.b\n  }\n}\n"
	if u := front.CompileWith("collections/auth.art", coll, nil); u.Bag.Len() != 0 {
		t.Fatalf("two writes of one name after the action are legal, got %v", u.Bag.All())
	}
}

// A context name only some branches of the flow's if write, read in an item:
// the message names the if by its line and its file, which is not the item's.
func TestMaybeUnboundNamesTheBlocksFile(t *testing.T) {
	files := expand.MapLoader{
		"api.art": "collection \"api\" {\n  request me {\n    var token\n    get \"http://h/me/${token}\"\n  }\n}\n",
		"f.art": "import \"api.art\"\n\nflow s {\n  var x = true\n  if x {\n    step \"a\" {\n      get \"http://h/\"\n" +
			"      var token = body.t\n    }\n  }\n  step \"me\" = api.me { }\n}\n",
	}
	u := front.CompileWith("main.art", "flow \"f.art\"\n\nscenario \"s\" { }\n", files)
	ds := u.Bag.All()
	if len(ds) != 1 || ds[0].Code != diag.CaptureMaybeUnbound {
		t.Fatalf("want one capture-maybe-unbound, got %v", ds)
	}
	if want := `"token" is written in only some branches of the if at line 5 of f.art, so it may not exist here`; ds[0].Message != want {
		t.Errorf("message = %q\nwant      %q", ds[0].Message, want)
	}
	if strings.Contains(ds[0].Hint, "captur") {
		t.Errorf("the hint still says capture: %q", ds[0].Hint)
	}
}

// missing-argument's hint names only the inputs still missing: one the
// flow's context supplies is not.
func TestMissingArgumentHintListsOnlyWhatIsMissing(t *testing.T) {
	files := expand.MapLoader{
		"c.art": "collection \"c\" {\n  request q {\n    var url\n    var user\n    get \"${url}/${user}\"\n  }\n}\n",
		"f.art": "import \"c.art\"\n\nflow s {\n  var url = \"http://h\"\n  step \"call\" = c.q { }\n}\n",
	}
	u := front.CompileWith("f.art", files["f.art"], files)
	ds := u.Bag.All()
	if len(ds) != 1 || ds[0].Code != diag.MissingArgument || ds[0].Message != "c.q needs user" {
		t.Fatalf("want one missing-argument for user, got %v", ds)
	}
	if strings.Contains(ds[0].Hint, "url") || !strings.Contains(ds[0].Hint, "user = ...") {
		t.Errorf("the hint must name user and not url: %q", ds[0].Hint)
	}
}

// A two-letter near miss of a parameter or an argument gets a did-you-mean.
func TestAShortNearMissIsSuggested(t *testing.T) {
	files := expand.MapLoader{
		"c.art":       "collection \"c\" {\n  request q {\n    var url\n    get url\n  }\n}\n",
		"flows/f.art": "import \"../c.art\"\n\nflow s {\n  var url = \"http://h\"\n  step \"call\" = c.q { ur = \"x\" }\n}\n",
	}
	u := front.CompileWith("scenarios/x.art", "flow \"../flows/f.art\"\n\nscenario \"s\" { ur = \"y\" }\n", files)
	got := map[diag.Code]string{}
	for _, d := range u.Bag.All() {
		got[d.Code] = d.Hint
	}
	for _, code := range []diag.Code{diag.UnknownArgument, diag.UnknownParam} {
		if got[code] != `did you mean "url"?` {
			t.Errorf("%s hint = %q, want did you mean \"url\"?", code, got[code])
		}
	}
}
