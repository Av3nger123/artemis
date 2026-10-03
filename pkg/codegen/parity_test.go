package codegen

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/dsl/token"
	"artemis/pkg/steps/browserstep/fixture"
)

// ART-49, the anti-drift test: the generated pytest and `artemis run` are given
// the same scenarios against the same fixture server, and a scenario the two
// disagree about fails this test rather than reaching a user.
//
// # Why both backends are subprocesses of the real binary
//
// The interpreter's run loop is runFilesWith in pkg/cli, which is unexported --
// and pkg/cli imports this package, so it could not be imported here even if it
// were. Reimplementing the loop would make this a test of a runner nobody runs.
// So the binary is built once per test binary and driven twice per corpus file:
// `artemis run --report json` for the interpreter's verdict and `artemis build
// --lang=python -o` for the module pytest then collects. Both commands as they
// ship, which is the point.
//
// # What is compared
//
// Pass versus not-pass, per scenario, three ways: the interpreter, the generated
// pytest, and the outcome the corpus itself declares. The two backends
// disagreeing is a backend divergence. The two agreeing on an answer the corpus
// does not declare is a rule that moved underneath both of them -- a lowering
// change, say -- which is the other half of what this test is for.
//
// Not the failure class, and not per step. The interpreter separates `fail` from
// `error` and pytest's JUnit reports every exception raised inside a test body
// as a failure, so a finer verdict would compare two things that are not the
// same thing. And pkg/codegen/python.go documents that pytest stops at the first
// failed assert while the interpreter evaluates every `expect`: at scenario
// granularity that difference does not exist, because any failure fails the
// scenario on both sides.
//
// # What the corpus may not do
//
// The three differences the generated file's own header lists are differences by
// design, so a corpus scenario must not depend on one: no comparison of a number
// against a string, no step without an explicit `timeout`, and no assertion on
// output long enough to be truncated.

// conformanceDir is the corpus every backend runs: the interpreter, the generated
// pytest here, and the generated vitest in jsparity_test.go. One directory, so a
// scenario the Python export handles and the JavaScript export does not is
// reported rather than never asked.
const conformanceDir = "testdata/conformance"

// browserConformanceDir is the browser half, run by parity_browser_test.go and
// jsparity_browser_test.go behind the `browser` build tag: it needs a real
// Chromium on the Go side and Playwright's own browsers on each export's.
const browserConformanceDir = conformanceDir + "/browser"

// directive is the comment that declares what a scenario does against the
// fixture server. It sits on its own line above the `scenario` it governs, so
// adding a scenario and forgetting its expectation cannot happen quietly.
//
//	# conformance: pass
//	scenario "the query reaches the page" { ... }
var directive = regexp.MustCompile(`^\s*#\s*conformance:\s*(pass|fail)\s*$`)

// outcome is as fine a verdict as the two backends can be compared at.
type outcome string

const (
	pass outcome = "pass"
	fail outcome = "fail"
)

// parityRow is one scenario's three answers.
//
// pyTest and jsTest are the names each export gives the scenario, which is what
// lines it up with a JUnit testcase. The Python one is computed with the
// emitter's own testName and unique rather than restated, so two scenarios whose
// names slug alike are mapped the way the generated module actually names them;
// the JavaScript one is the scenario's name, because a vitest test is named by a
// string and there is nothing to slug.
//
// exported is whichever export this run drove. The two are never compared with
// each other -- a divergence between two exports and a divergence between an
// export and the interpreter are the same bug, and the interpreter is the
// authority both are measured against.
type parityRow struct {
	scenario string
	pyTest   string
	jsTest   string
	declared outcome
	interp   outcome
	exported outcome
}

// TestPythonExecutionParity is the issue: run both backends over the corpus and
// compare.
func TestPythonExecutionParity(t *testing.T) {
	parity(t, conformanceDir, nil)
}

// parity runs every .art file in dir both ways against a fresh fixture server
// and returns every problem the comparison found.
//
// mutate, when it is not nil, rewrites the generated Python before pytest sees
// it. Only TestParityDetectsADivergentBackend passes one.
func parity(t *testing.T, dir string, mutate func(string) string, modules ...string) []string {
	t.Helper()
	bin, python, env := parityEnv(t, modules...)

	var problems []string
	for _, name := range corpusIn(t, dir) {
		file := abs(t, filepath.Join(dir, name+artExt))
		t.Run(name+artExt, func(t *testing.T) {
			found := parityOf(t, bin, python, file, env, mutate)
			problems = append(problems, found...)
			for _, p := range found {
				t.Error(p)
			}
		})
	}
	return problems
}

// parityEnv is what both backends need: the binary, the python interpreter, and
// an environment holding ARTEMIS_BASE_URL for a fixture server that lives as long as the
// test does.
//
// ARTEMIS_BASE_URL is how the corpus reaches the server -- `var base = env("ARTEMIS_BASE_URL")`
// is read by eval.Env.getenv under the interpreter and by os.environ.get in the
// generated module, so one variable parameterises both backends and the corpus
// hard-codes no port.
func parityEnv(t *testing.T, modules ...string) (bin, python string, env []string) {
	t.Helper()
	python = pythonWith(t, append([]string{"pytest", "requests"}, modules...)...)
	bin = artemisBinary(t)
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)
	return bin, python, append(os.Environ(), "ARTEMIS_BASE_URL="+srv.URL)
}

// parityOf runs one corpus file both ways and compares the two verdicts against
// each other and against what the file declares.
func parityOf(t *testing.T, bin, python, file string, env []string, mutate func(string) string) []string {
	t.Helper()
	rows, undeclared := scenarios(t, file)
	if len(undeclared) > 0 {
		t.Fatalf("%s: no `# conformance: pass|fail` above %s",
			filepath.Base(file), strings.Join(undeclared, ", "))
	}
	interp := interpret(t, bin, file, env)
	py := pytest(t, python, bin, file, env, mutate)
	for i := range rows {
		if _, ok := interp[rows[i].scenario]; !ok {
			t.Fatalf("%s: artemis run reported no scenario named %q", filepath.Base(file), rows[i].scenario)
		}
		if _, ok := py[rows[i].pyTest]; !ok {
			t.Fatalf("%s: pytest collected no test named %s for scenario %q",
				filepath.Base(file), rows[i].pyTest, rows[i].scenario)
		}
		rows[i].interp = interp[rows[i].scenario]
		rows[i].exported = py[rows[i].pyTest]
	}
	return compare(filepath.Base(file), "pytest", rows)
}

func abs(t *testing.T, path string) string {
	t.Helper()
	full, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return full
}

// compare is the three-way comparison, and the only place that decides what a
// mismatch is called.
//
// It is a function over rows rather than a block of t.Errorf calls so that
// TestParityDetectsADivergentBackend can assert it reports a divergence when
// there is one -- a harness that can only ever agree proves nothing.
//
// runner names the export being measured -- "pytest" or "vitest" -- so one
// function serves both backends and the two can never drift into two different
// ideas of what a mismatch is.
func compare(file, runner string, rows []parityRow) []string {
	var problems []string
	for _, r := range rows {
		switch {
		case r.interp != r.exported:
			problems = append(problems, fmt.Sprintf(
				"%s: scenario %q diverges: `artemis run` says %s and the generated %s says %s "+
					"(the export and the interpreter disagree about the same scenario)",
				file, r.scenario, r.interp, runner, r.exported))
		case r.interp != r.declared:
			problems = append(problems, fmt.Sprintf(
				"%s: scenario %q is declared to %s, and both the interpreter and the generated "+
					"%s %sed it (a rule moved underneath both backends, or the corpus is wrong)",
				file, r.scenario, r.declared, runner, r.interp))
		}
	}
	return problems
}

// scenarios reads one corpus file: a row per scenario with its declared outcome
// and the test function it becomes, plus the names of any scenario with no
// directive above it.
//
// The scenario names and lines come from the front end and the lowering -- the
// same tree the target is handed -- and the directive is matched to the scenario
// below it by line. Reading the names off the tree rather than out of the text
// is what makes an escape or an unusual character in a scenario name a
// non-event.
func scenarios(t *testing.T, path string) (rows []parityRow, undeclared []string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[int]outcome{}
	for i, line := range strings.Split(string(src), "\n") {
		if m := directive.FindStringSubmatch(line); m != nil {
			// The scenario it governs is the next one below it, so the
			// directive is keyed by its own 1-based line.
			declared[i+1] = outcome(m[1])
		}
	}

	lowered, err := lower.File(parse(t, path, string(src)), nil)
	if err != nil {
		t.Fatalf("%s: lower: %v", path, err)
	}
	taken := map[string]bool{}
	seenName := map[string]bool{}
	for _, sc := range lowered {
		if seenName[sc.Name] {
			// Two scenarios with one name would make the interpreter's
			// per-scenario verdict ambiguous, and this test is the only thing
			// that would notice.
			t.Fatalf("%s: two scenarios are both named %q", path, sc.Name)
		}
		seenName[sc.Name] = true

		row := parityRow{
			scenario: sc.Name,
			pyTest:   unique(testName(sc.Name), taken),
			jsTest:   sc.Name,
		}
		if o, ok := nearest(declared, sc.Line); ok {
			row.declared = o
		} else {
			undeclared = append(undeclared, fmt.Sprintf("scenario %q (line %d)", sc.Name, sc.Line))
			continue
		}
		rows = append(rows, row)
	}
	return rows, undeclared
}

// nearest is the directive that governs the scenario on line: the closest one
// above it, with nothing but blank lines and comments in between -- which is
// everything a directive can be separated from its scenario by, since a
// scenario's own line is where its keyword is.
func nearest(declared map[int]outcome, line int) (outcome, bool) {
	for n := line - 1; n > 0; n-- {
		if o, ok := declared[n]; ok {
			return o, true
		}
	}
	return "", false
}

// interpret runs `artemis run` over one file and returns each scenario's
// verdict.
//
// --report json to a file rather than to stdout, so the console summary stays
// readable in a failing test's log, and the run's own non-zero exit is expected:
// the corpus declares failures on purpose. What is not expected is a report that
// is not there, which is what the error message is about.
func interpret(t *testing.T, bin, file string, env []string) map[string]outcome {
	t.Helper()
	dir := t.TempDir()
	report := filepath.Join(dir, "run.json")
	cmd := exec.Command(bin, "run", file, "--report", "json="+report) //nolint:gosec // bin is the binary this test built
	cmd.Dir = dir
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()

	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("artemis run wrote no report (%v): %v\n%s", err, runErr, out)
	}
	var doc struct {
		Scenarios []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reading %s: %v", report, err)
	}
	if len(doc.Scenarios) == 0 {
		t.Fatalf("artemis run reported no scenarios for %s:\n%s", file, out)
	}

	verdicts := make(map[string]outcome, len(doc.Scenarios))
	for _, sc := range doc.Scenarios {
		// skip counts as pass, which is what result.Status.ok says: a scenario
		// that did not run is not a scenario that answered wrong.
		verdicts[sc.Name] = verdict(sc.Status == "pass" || sc.Status == "skip")
	}
	return verdicts
}

// pytest generates the module with `artemis build` and runs it, returning each
// test function's verdict.
//
// --junitxml rather than the exit code or the summary line, because the question
// is per scenario and a JUnit testcase is the only per-test answer pytest gives
// a program. -p no:cacheprovider keeps the run from leaving a .pytest_cache
// behind in the temporary directory it is told to work in.
func pytest(t *testing.T, python, bin, file string, env []string, mutate func(string) string) map[string]outcome {
	t.Helper()
	dir := t.TempDir()

	build := exec.Command(bin, "build", "--lang=python", "-o", dir, file) //nolint:gosec // bin is the binary this test built
	build.Dir = dir
	build.Env = env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("artemis build %s: %v\n%s", file, err, out)
	}
	if mutate != nil {
		mutateModules(t, dir, mutate)
	}

	report := filepath.Join(dir, "pytest.xml")
	cmd := exec.Command(python, "-m", "pytest", dir, //nolint:gosec // python came from exec.LookPath
		"-q", "-p", "no:cacheprovider", "--junitxml="+report)
	cmd.Dir = dir
	cmd.Env = append(env, "PYTHONDONTWRITEBYTECODE=1")
	out, runErr := cmd.CombinedOutput()

	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("pytest wrote no report (%v): %v\n%s", err, runErr, out)
	}
	var doc struct {
		Suites []struct {
			Cases []struct {
				Name     string     `xml:"name,attr"`
				Failures []struct{} `xml:"failure"`
				Errors   []struct{} `xml:"error"`
				Skipped  []struct{} `xml:"skipped"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reading %s: %v", report, err)
	}

	verdicts := map[string]outcome{}
	for _, suite := range doc.Suites {
		for _, c := range suite.Cases {
			verdicts[c.Name] = verdict(len(c.Failures) == 0 && len(c.Errors) == 0)
		}
	}
	if len(verdicts) == 0 {
		// A module that does not import collects nothing, and a comparison
		// against nothing would look like a divergence with no cause. pytest's
		// own output is the only thing that says why.
		t.Fatalf("pytest collected no tests from the module generated for %s:\n%s", file, out)
	}
	return verdicts
}

// mutateModules rewrites every generated module in dir, and fails if the
// mutation changed nothing -- a mutation test whose mutation did not apply is a
// test that passes for the wrong reason.
func mutateModules(t *testing.T, dir string, mutate func(string) string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "test_*.py"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no generated module in %s: %v", dir, err)
	}
	changed := false
	for _, p := range paths {
		src, err := os.ReadFile(p) //nolint:gosec // p came from a glob of a temporary directory
		if err != nil {
			t.Fatal(err)
		}
		after := mutate(string(src))
		if after != string(src) {
			changed = true
		}
		if err := os.WriteFile(p, []byte(after), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !changed {
		t.Fatalf("the mutation matched nothing in %s", dir)
	}
}

func verdict(ok bool) outcome {
	if ok {
		return pass
	}
	return fail
}

// Every scenario in the corpus declares an outcome. Its own test, so a
// scenario added without a directive is reported as the omission it is rather
// than as whatever the comparison made of it.
func TestEveryConformanceScenarioDeclaresAnOutcome(t *testing.T) {
	for _, dir := range conformanceDirs() {
		for _, name := range corpusIn(t, dir) {
			path := filepath.Join(dir, name+artExt)
			rows, undeclared := scenarios(t, path)
			if len(undeclared) > 0 {
				t.Errorf("%s: no `# conformance: pass|fail` above %s",
					path, strings.Join(undeclared, ", "))
			}
			if len(rows) == 0 && len(undeclared) == 0 {
				t.Errorf("%s holds no scenario", path)
			}
		}
	}
}

// Both outcomes are declared somewhere in the corpus. Without a declared
// failure the whole harness could pass by never detecting a failure at all,
// which is the one way an anti-drift test can be useless and still be green.
func TestTheConformanceCorpusDeclaresBothOutcomes(t *testing.T) {
	counts := map[outcome]int{}
	for _, dir := range conformanceDirs() {
		for _, name := range corpusIn(t, dir) {
			rows, _ := scenarios(t, filepath.Join(dir, name+artExt))
			for _, r := range rows {
				counts[r.declared]++
			}
		}
	}
	for _, want := range []outcome{pass, fail} {
		if counts[want] == 0 {
			t.Errorf("no scenario in %s is declared to %s", conformanceDir, want)
		}
	}
}

// The corpus covers what the issue's done-when asks for, read out of the token
// tables rather than claimed in a comment: every operator, and both non-browser
// step types. The browser flow is the tagged file's, and `browser` is excused
// here by name for that reason.
func TestTheConformanceCorpusCoversEveryOperator(t *testing.T) {
	got := walkCorpus(t, conformanceDirs()...)

	missing(t, conformanceDir, "comparison operator", token.Comparisons, got.ops)
	missing(t, conformanceDir, "word operator", token.WordOperators, got.ops)

	// Both non-browser step types, by their roots: an api step binds status,
	// body, raw and headers and a terminal step exit_code, stdout and stderr,
	// so a corpus that asserts on all of them has exercised both.
	for _, typ := range check.StepTypes() {
		if typ == check.Browser {
			continue
		}
		missing(t, conformanceDir, "root of a "+typ.String()+" step", check.Roots(typ), got.idents)
	}
	if !got.methods["get"] || !got.methods["post"] {
		t.Error("the corpus drives no GET and no POST; an api step's request is half of what diverges")
	}
	if !got.actions["run"] {
		t.Error("the corpus holds no terminal step")
	}
	// And at least one browser flow, which parity_browser_test.go runs. Its
	// presence is checked here rather than there, so a corpus that lost its
	// browser flow is reported by the default suite instead of by a tagged one
	// nobody ran.
	if !got.actions["browser"] {
		t.Errorf("the corpus holds no browser flow; one belongs in %s", browserConformanceDir)
	}
}

// conformanceDirs is every corpus directory. Both are read whatever the build
// tags are: the browser flow's declarations, its coverage and the fact that it
// compiles are the default suite's business, and only *running* it needs a
// browser.
func conformanceDirs() []string { return []string{conformanceDir, browserConformanceDir} }

// pythonWith is the interpreter to run the generated tests with, or a skip
// naming what to install.
//
// Skipping rather than failing is how `make lint` treats a missing linter and
// how TestGoldensAreValidPython treats a missing python3: a toolchain nobody
// asked for must not stop someone building the binary. CI installs all of it, so
// the gate is real where it counts -- see .github/workflows/ci.yml.
func pythonWith(t *testing.T, modules ...string) string {
	t.Helper()
	// Its own lookup rather than python3(t), only so that the note says which
	// test is being skipped: a skip whose reason is somebody else's test is a
	// skip nobody acts on.
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 is not installed; skipping the execution-parity test (%v)", err)
	}
	for _, m := range modules {
		if err := exec.Command(python, "-c", "import "+m).Run(); err != nil { //nolint:gosec // python came from exec.LookPath
			t.Skipf("the python %s module is not installed; skipping the execution-parity test "+
				"(install it with `%s -m pip install %s`)", m, python, strings.Join(modules, " "))
		}
	}
	return python
}

// The harness has to be able to report a divergence, or every green run of it
// means nothing. So one is produced on purpose: the generated module for
// api.art is rewritten to assert a status the fixture server does not return,
// which is what a lowering rule or an emitter rule that moved for the python
// backend alone would look like. The interpreter still passes the scenario, and
// compare must say the two disagree.
//
// The mutation is applied to the generated source rather than to the emitter,
// because the emitter has no seam to break at run time -- and this is the
// failure a reader cares about: the backends differ, whatever made them differ.
func TestParityDetectsADivergentBackend(t *testing.T) {
	bin, python, env := parityEnv(t)
	file := abs(t, filepath.Join(conformanceDir, "api"+artExt))

	problems := parityOf(t, bin, python, file, env, func(src string) string {
		return strings.ReplaceAll(src, "assert status == 200", "assert status == 500")
	})
	if len(problems) == 0 {
		t.Fatal("a mutated python module agreed with the interpreter; this test cannot detect a divergent backend")
	}
	for _, p := range problems {
		if strings.Contains(p, "diverges") {
			return
		}
	}
	t.Errorf("the mutation was reported, but not as a divergence: %s", strings.Join(problems, "\n"))
}

// The binary both backends are driven through, built once per test binary.
var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// artemisBinary builds ./cmd/cli and returns the path to it.
//
// Once per test binary, into a directory TestMain removes: the two backends and
// every corpus file share one build, and `go build`'s own cache makes the first
// one cheap on a machine that has already built the module.
func artemisBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		goTool, err := exec.LookPath("go")
		if err != nil {
			buildErr = fmt.Errorf("the go toolchain is not on PATH, so `artemis` cannot be built: %w", err)
			return
		}
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "artemis-parity-")
		if err != nil {
			buildErr = err
			return
		}
		binDir = dir
		bin := filepath.Join(dir, "artemis")
		cmd := exec.Command(goTool, "build", "-o", bin, "./cmd/cli") //nolint:gosec // goTool came from exec.LookPath
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/cli: %w\n%s", err, out)
			return
		}
		builtBin = bin
	})
	if buildErr != nil {
		t.Fatalf("building the artemis binary: %v", buildErr)
	}
	return builtBin
}

// binDir is what TestMain cleans up. A test's own t.TempDir would go while
// another test still needed the binary.
var binDir string

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}
