package migrate

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"
)

// readTree is every file under dir, by slash path relative to it.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // a test fixture
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// fixture is one case's input, built.
func fixture(t *testing.T, name string) (map[string]*ast.File, []string) {
	t.Helper()
	files, notes, err := build(readTree(t, filepath.Join("testdata", name, "in")))
	if err != nil {
		t.Fatalf("build(%s) = %v", name, err)
	}
	return files, notes
}

func paths(files map[string]*ast.File) []string {
	var out []string
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func wantPaths(t *testing.T, files map[string]*ast.File, want ...string) {
	t.Helper()
	if got := paths(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
}

// flowOf is the flow a flow file holds, and its imports' paths.
func flowOf(t *testing.T, f *ast.File) (*ast.Flow, []string) {
	t.Helper()
	var flow *ast.Flow
	var imports []string
	for _, d := range f.Scenarios {
		switch v := d.(type) {
		case *ast.Flow:
			if flow != nil {
				t.Fatal("two flows in one file")
			}
			flow = v
		case *ast.Import:
			imports = append(imports, v.Path.Value)
		default:
			t.Fatalf("a flow file holds a %T", d)
		}
	}
	if flow == nil {
		t.Fatal("no flow in the file")
	}
	return flow, imports
}

// scenariosOf is a scenario file's flow path and its scenarios.
func scenariosOf(t *testing.T, f *ast.File) (string, []*ast.Scenario) {
	t.Helper()
	ref, ok := f.Scenarios[0].(*ast.FlowRef)
	if !ok {
		t.Fatalf("a scenario file opens with %T, want *ast.FlowRef", f.Scenarios[0])
	}
	var out []*ast.Scenario
	for _, d := range f.Scenarios[1:] {
		s, ok := d.(*ast.Scenario)
		if !ok {
			t.Fatalf("a scenario file holds a %T", d)
		}
		for _, b := range s.Body {
			switch b.(type) {
			case *ast.Set, *ast.TagsDecl:
			default:
				t.Errorf("scenario %s holds a %T; a scenario holds data only", s.Name.Text, b)
			}
		}
		out = append(out, s)
	}
	return ref.Path.Value, out
}

// shape is a body's declarations as words: "var url", "step pay", "setup",
// "use bad=auth.login".
func shape(body []ast.Decl) []string {
	var out []string
	for _, d := range body {
		switch v := d.(type) {
		case *ast.VarDecl:
			out = append(out, "var "+v.Name.Value)
		case *ast.StepDecl:
			out = append(out, "step "+ast.Display(v.Name, v.Label))
		case *ast.UseDecl:
			out = append(out, "step "+ast.Display(v.Name, v.Label)+" = "+v.Ref())
		case *ast.PhaseDecl:
			out = append(out, v.Keyword.Value+"("+strings.Join(shape(v.Body), ", ")+")")
		case *ast.ConfigDecl:
			out = append(out, "config "+v.Subject.Value)
		default:
			out = append(out, reflect.TypeOf(d).String())
		}
	}
	return out
}

func wantShape(t *testing.T, body []ast.Decl, want ...string) {
	t.Helper()
	if got := shape(body); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// expr is x printed: the canonical printer handles every expression already.
func expr(x ast.Expr) string { return strings.TrimSpace(print.Canonical(x)) }

func tagsOf(t *testing.T, s *ast.Scenario) string {
	t.Helper()
	for _, b := range s.Body {
		if tg, ok := b.(*ast.TagsDecl); ok {
			return expr(tg.Value)
		}
	}
	return ""
}

func step(t *testing.T, body []ast.Decl, name string) *ast.StepDecl {
	t.Helper()
	for _, d := range body {
		if s, ok := d.(*ast.StepDecl); ok && ast.Display(s.Name, s.Label) == name {
			return s
		}
		if p, ok := d.(*ast.PhaseDecl); ok {
			for _, b := range p.Body {
				if s, ok := b.(*ast.StepDecl); ok && ast.Display(s.Name, s.Label) == name {
					return s
				}
			}
		}
	}
	t.Fatalf("no step %q", name)
	return nil
}

func captures(s *ast.StepDecl) []string {
	var out []string
	for _, st := range s.Body {
		if c, ok := st.(*ast.Capture); ok {
			out = append(out, c.Keyword.Value+" "+c.Name.Value+" = "+expr(c.Value))
		}
	}
	return out
}

func wantNotes(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notes = %q\nwant %q", got, want)
	}
}

// A scenario becomes a flow named for its file, with its vars at the top --
// one written after a step too -- and a scenario file holding one empty
// scenario with the old title and tags. A data path is rewritten for the
// flow's folder, and the comment above the scenario is above the flow.
func TestScenarioBecomesAFlowAndAScenarioFile(t *testing.T) {
	files, notes := fixture(t, "scenario")
	wantPaths(t, files, "flows/checkout.art", "scenarios/checkout.art")
	wantNotes(t, notes)

	flow, imports := flowOf(t, files["flows/checkout.art"])
	if flow.Name.Value != "checkout" || len(imports) != 0 {
		t.Errorf("flow %s with imports %q, want checkout and none", flow.Name.Value, imports)
	}
	if cs := strings.TrimSpace(triviaText(flow.Keyword.Leading)); cs != "# Buys one cart." {
		t.Errorf("comment above the flow = %q", cs)
	}
	wantShape(t, flow.Body, "var url", "var currency", "step pay")
	pay := step(t, flow.Body, "pay")
	if got := print.Canonical(pay.Action); !strings.Contains(got, `file("../data/cart.json")`) {
		t.Errorf("the step's body reads %s, want the path from flows/", got)
	}

	ref, scs := scenariosOf(t, files["scenarios/checkout.art"])
	if ref != "../flows/checkout.art" || len(scs) != 1 {
		t.Fatalf("scenario file names %s with %d scenarios", ref, len(scs))
	}
	if ast.Display(scs[0].Name, scs[0].Label) != "checkout" || tagsOf(t, scs[0]) != `["smoke"]` || len(scs[0].Body) != 1 {
		t.Errorf("scenario %s with tags %s and %d lines", scs[0].Name.Text, tagsOf(t, scs[0]), len(scs[0].Body))
	}
}

func triviaText(tr []token.Trivia) string {
	var b strings.Builder
	token.WriteTrivia(&b, tr)
	return b.String()
}

// collectionItem is the item of a collection file named name.
func collectionItem(t *testing.T, f *ast.File, name string) *ast.RequestDecl {
	t.Helper()
	for _, d := range f.Scenarios {
		if c, ok := d.(*ast.Collection); ok {
			if r := find(c, name); r != nil {
				return r
			}
		}
	}
	t.Fatalf("no item %s", name)
	return nil
}

// inputs is an item's inputs as a parameter list would be written.
func inputs(r *ast.RequestDecl) string {
	var parts []string
	for _, v := range r.Inputs {
		s := v.Name.Value
		if !v.Required() {
			s += " = " + expr(v.Value)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// A file of two scenarios is two flows and two scenario files, numbered in
// the order they are written.
func TestSeveralScenariosAreNumbered(t *testing.T) {
	files, _ := fixture(t, "several")
	wantPaths(t, files, "flows/login_1.art", "flows/login_2.art", "scenarios/login_1.art", "scenarios/login_2.art")
	for i, title := range []string{"good password", "bad password"} {
		n := []string{"login_1", "login_2"}[i]
		flow, _ := flowOf(t, files["flows/"+n+".art"])
		if flow.Name.Value != n {
			t.Errorf("flow %s, want %s", flow.Name.Value, n)
		}
		ref, scs := scenariosOf(t, files["scenarios/"+n+".art"])
		if ref != "../flows/"+n+".art" || ast.Display(scs[0].Name, scs[0].Label) != title {
			t.Errorf("scenarios/%s.art names %s and %q", n, ref, ast.Display(scs[0].Name, scs[0].Label))
		}
	}
}

// A capture is a var after the action, keeping the comment above it; a use is
// a step named as the old use's step was, with its lines, and drop captures
// is drop vars. The collection it reads moves to var inputs.
func TestCaptureAndUseBecomeVarAndStep(t *testing.T) {
	files, _ := fixture(t, "capture_use")
	wantPaths(t, files, "collections/auth.art", "flows/checkout.art", "scenarios/checkout.art")

	flow, imports := flowOf(t, files["flows/checkout.art"])
	if !reflect.DeepEqual(imports, []string{"../collections/auth.art"}) {
		t.Errorf("imports = %q, want the path from flows/", imports)
	}
	wantShape(t, flow.Body, "step bad = auth.login", "step auth.login = auth.login", "step order", "step pay")

	bad := flow.Body[0].(*ast.UseDecl)
	if bad.Keyword.Text != "" || bad.Step.Value != "step" || bad.Alias.Value != "bad" {
		t.Errorf("use as bad = step %q keyword %q alias %q", bad.Step.Value, bad.Keyword.Text, bad.Alias.Value)
	}
	var drops []string
	for _, l := range bad.Lines {
		if d, ok := l.(*ast.Drop); ok {
			drops = append(drops, d.What.Value)
		}
	}
	if !reflect.DeepEqual(drops, []string{"expects", "vars"}) {
		t.Errorf("drops = %q, want expects and vars", drops)
	}

	order := step(t, flow.Body, "order")
	if got := captures(order); !reflect.DeepEqual(got, []string{"var order_id = body.id"}) {
		t.Errorf("captures = %q", got)
	}
	var c *ast.Capture
	for _, st := range order.Body {
		if v, ok := st.(*ast.Capture); ok {
			c = v
		}
	}
	if !c.IsVar() || !strings.Contains(triviaText(c.Keyword.Leading), "# the order to pay") {
		t.Errorf("the capture's var lost its comment: %q", triviaText(c.Keyword.Leading))
	}

	login := collectionItem(t, files["collections/auth.art"], "login")
	if login.Params != nil || inputs(login) != "user, password" {
		t.Errorf("login inputs = %q, params %v", inputs(login), login.Params)
	}
	if got := captures(&ast.StepDecl{Body: login.Body}); !reflect.DeepEqual(got, []string{"var token = body.token"}) {
		t.Errorf("login captures = %q", got)
	}
}

// A for scenario is one scenario for each row, named as the row's scenario was,
// each setting the row var; the flow declares the row var with the first row.
func TestRowsBecomeScenarios(t *testing.T) {
	files, _ := fixture(t, "rows")
	wantPaths(t, files, "flows/login.art", "scenarios/login.art")

	flow, _ := flowOf(t, files["flows/login.art"])
	wantShape(t, flow.Body, "var user", "step log in")
	first := `{"email": "a@x.io", "role": "admin", "status": 200}`
	if got := expr(flow.Body[0].(*ast.VarDecl).Value); got != first {
		t.Errorf("var user = %s, want %s", got, first)
	}

	_, scs := scenariosOf(t, files["scenarios/login.art"])
	want := map[string]string{
		"login as admin": first,
		"login as guest": `{"email": "g@x.io", "role": "guest", "status": 403}`,
	}
	if len(scs) != 2 {
		t.Fatalf("%d scenarios, want 2", len(scs))
	}
	for _, s := range scs {
		set := s.Body[0].(*ast.Set)
		if set.Name.Value != "user" || expr(set.Value) != want[ast.Display(s.Name, s.Label)] {
			t.Errorf("scenario %q sets %s = %s", ast.Display(s.Name, s.Label), set.Name.Value, expr(set.Value))
		}
	}
}

// A collection's params are var inputs with their defaults, a run item is a
// command, and secret is removed with a note for each.
func TestCollectionParamsBecomeInputs(t *testing.T) {
	files, notes := fixture(t, "collection")
	wantPaths(t, files, "ops.art")
	wantNotes(t, notes,
		"ops.art:2: secret is removed from the input password of login, and reports no longer withhold its value",
		"ops.art:7: secret is removed from capture token, and reports no longer withhold its value")

	login := collectionItem(t, files["ops.art"], "login")
	if login.Keyword.Value != "request" || inputs(login) != `user, password, base = env("API_URL")` {
		t.Errorf("login = %s with %s", login.Keyword.Value, inputs(login))
	}
	if got := captures(&ast.StepDecl{Body: login.Body}); !reflect.DeepEqual(got, []string{"var token = body.token"}) {
		t.Errorf("login captures = %q", got)
	}
	migrate := collectionItem(t, files["ops.art"], "migrate")
	if migrate.Keyword.Value != "command" || migrate.Type() != "terminal" || inputs(migrate) != `target = "migrate"` {
		t.Errorf("migrate = %s with %s", migrate.Keyword.Value, inputs(migrate))
	}
}

// secret var and secret capture are plain, each with a note, and the comment
// above `secret var` stays above `var`.
func TestSecretIsRemovedWithANote(t *testing.T) {
	files, notes := fixture(t, "secret")
	wantNotes(t, notes,
		"checkout.art:3: secret is removed from var pw, and reports no longer withhold its value",
		"checkout.art:10: secret is removed from capture token, and reports no longer withhold its value")
	flow, _ := flowOf(t, files["flows/checkout.art"])
	pw := flow.Body[0].(*ast.VarDecl)
	if pw.Secret.Text != "" || !strings.Contains(triviaText(pw.Keyword.Leading), "# the account's password") {
		t.Errorf("var pw = secret %q, comment %q", pw.Secret.Text, triviaText(pw.Keyword.Leading))
	}
	if got := captures(step(t, flow.Body, "get a token")); !reflect.DeepEqual(got, []string{"var token = body.token"}) {
		t.Errorf("captures = %q", got)
	}
}

// A suite's vars, setup and teardown are copied into each flow under it, its
// tags onto each scenario, suite.x reads x, and the suite file is not written.
func TestSuiteIsCopiedIntoEachFlow(t *testing.T) {
	files, notes := fixture(t, "suite")
	wantPaths(t, files, "suite/orders/flows/create.art", "suite/orders/scenarios/create.art")
	wantNotes(t, notes, "suite/suite.art: its setup now runs once per scenario, not once per run")

	flow, _ := flowOf(t, files["suite/orders/flows/create.art"])
	wantShape(t, flow.Body, "var url", "setup(step log in)", "step create", "teardown(step log out)")
	got := print.Canonical(step(t, flow.Body, "create").Action)
	if !strings.Contains(got, `"${url}/orders"`) || !strings.Contains(got, `"Bearer ${token}"`) {
		t.Errorf("create sends %s, want suite.url and suite.token read as url and token", got)
	}
	_, scs := scenariosOf(t, files["suite/orders/scenarios/create.art"])
	if tagsOf(t, scs[0]) != `["api", "orders"]` {
		t.Errorf("tags = %s, want the suite's then the scenario's", tagsOf(t, scs[0]))
	}
}

// An input the old use left to its default, whose name the flow also binds,
// is passed its default at the step: a step would otherwise send the flow's.
func TestDefaultIsPinnedWhenTheFlowBindsItsName(t *testing.T) {
	files, notes := fixture(t, "pin")
	wantNotes(t, notes)
	flow, _ := flowOf(t, files["flows/order.art"])
	u := flow.Body[1].(*ast.UseDecl)
	var args []string
	for _, l := range u.Lines {
		f := l.(*ast.Field)
		args = append(args, f.Name.Value+" = "+expr(f.Value))
	}
	if !reflect.DeepEqual(args, []string{`sku = "A-1"`, "qty = 1"}) {
		t.Errorf("args = %q, want qty pinned to its default", args)
	}
}

// A suite above the root -- the CLI passes one as ../suite.art when it
// migrates a single file -- applies, and is not written.
func TestSuiteAboveTheRootApplies(t *testing.T) {
	in := map[string]string{
		"../suite.art": "suite {\n  var url = \"http://x\"\n}\n",
		"a.art":        "scenario \"a\" {\n  step \"s\" {\n    get \"${suite.url}/a\"\n  }\n}\n",
	}
	files, notes, err := build(in)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths(t, files, "flows/a.art", "scenarios/a.art")
	wantNotes(t, notes)
	flow, _ := flowOf(t, files["flows/a.art"])
	wantShape(t, flow.Body, "var url", "step s")
}

// A file that holds no scenario and no collection is printed as it is.
func TestOtherFilesPassThrough(t *testing.T) {
	in := map[string]string{"only.art": "import \"x.art\"\n"}
	files, _, err := build(in)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths(t, files, "only.art")
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   map[string]string
		want string
	}{
		{"a file that does not parse", map[string]string{"a.art": "scenario \"x\" {\n  step\n"}, "a.art:"},
		{"two outputs at one path",
			map[string]string{"a.art": "scenario \"a\" {\n}\nscenario \"b\" {\n}\n", "a_1.art": "scenario \"c\" {\n}\n"},
			"flows/a_1.art: two files would be written here"},
		{"a suite var the scenario declares too",
			map[string]string{"suite.art": "suite {\n  var url = 1\n}\n", "a.art": "scenario \"a\" {\n  var url = 2\n}\n"},
			"var url is declared by both suite.art and the scenario"},
		{"rows that cannot be read",
			map[string]string{"a.art": "scenario \"a\" for u in json(\"no.json\") {\n}\n"},
			"a.art:1:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Files(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Files() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestRebase(t *testing.T) {
	for _, tc := range []struct{ p, from, to, want string }{
		{"data/x.json", ".", "flows", "../data/x.json"},
		{"collections/a.art", "a", "a/flows", "../collections/a.art"},
		{"../c.art", "suite", "suite/orders/flows", "../../../c.art"},
		{"x.json", "suite", "suite/orders/flows", "../../x.json"},
		{"/abs/x.json", ".", "flows", "/abs/x.json"},
		{"../../x.json", "a", "a/flows", "../../../x.json"},
	} {
		if got := rebase(tc.p, tc.from, tc.to); got != tc.want {
			t.Errorf("rebase(%q, %q, %q) = %q, want %q", tc.p, tc.from, tc.to, got, tc.want)
		}
	}
}

// The byte-for-byte check: each case's out/ is what Files prints, and its
// notes.txt is the notes.
func TestMigrateFixtures(t *testing.T) {
	cases, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata", c.Name())
			got, notes, err := Files(readTree(t, filepath.Join(dir, "in")))
			if err != nil {
				t.Fatal(err)
			}
			want := readTree(t, filepath.Join(dir, "out"))
			if !reflect.DeepEqual(keys(got), keys(want)) {
				t.Fatalf("files = %q, want %q", keys(got), keys(want))
			}
			for k, w := range want {
				if got[k] != w {
					t.Errorf("%s =\n%s\nwant\n%s", k, got[k], w)
				}
			}
			wantNotes(t, notes, readNotes(t, filepath.Join(dir, "notes.txt"))...)
		})
	}
}

// Every scenario file each case's out/ holds compiles with no error, with the
// input's data files beside it.
func TestMigratedOutputCompiles(t *testing.T) {
	cases, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata", c.Name())
			in := readTree(t, filepath.Join(dir, "in"))
			out, _, err := Files(in)
			if err != nil {
				t.Fatal(err)
			}
			all := expand.MapLoader{}
			for k, v := range in {
				if !front.IsArtFile(k) {
					all[k] = v
				}
			}
			for k, v := range out {
				all[k] = v
			}
			for k, src := range out {
				if path.Base(path.Dir(k)) != "scenarios" {
					continue
				}
				u := front.CompileWith(k, src, all)
				if u.Bag.HasErrors() {
					t.Errorf("%s does not compile:\n%v\n%s", k, u.Bag.All(), src)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readNotes(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // a test fixture
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
