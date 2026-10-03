package codegen

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/steps/browserstep/fixture"
)

// The anti-drift test for the JavaScript target: the generated vitest and
// `artemis run` are given the same scenarios against the same fixture server,
// and a scenario the two disagree about fails this test rather than reaching a
// user.
//
// It is parity_test.go's harness over the same corpus, and deliberately not a
// second corpus. testdata/conformance is read by both, every scenario in it
// declares one outcome, and compare() is the one function that decides what a
// mismatch is -- so "the shared conformance corpus is the same one" is a fact
// about the code rather than a note in a README.
//
// # What is compared
//
// The same three answers, per scenario: the interpreter, the generated vitest,
// and the outcome the corpus declares. The two exports are never compared with
// each other; the interpreter is the authority both are measured against, so a
// divergence names one export and the scenario rather than leaving a reader to
// work out which of two is wrong.
//
// # Why the generated module is run through a linked node_modules
//
// vitest resolves `vitest` and `@playwright/test` from the test file's own
// directory upward, and the generated module lands in a temporary directory with
// no package of its own. So the temporary directory gets a package.json marking
// it ESM and a node_modules symlinked to the install vitest was found in. That is
// the whole adaptation: no generated package.json ships with `artemis build`, and
// the module a user gets is the module this runs.

// TestJSExecutionParity is the issue's execution gate: run the corpus both ways
// and compare.
func TestJSExecutionParity(t *testing.T) {
	jsParity(t, conformanceDir, nil)
}

// jsParity runs every .art file in dir both ways against a fresh fixture server
// and returns every problem the comparison found.
//
// mutate, when it is not nil, rewrites the generated JavaScript before vitest
// sees it. Only TestJSParityDetectsADivergentBackend passes one.
func jsParity(t *testing.T, dir string, mutate func(string) string, modules ...string) []string {
	t.Helper()
	bin, js, env := jsParityEnv(t, modules...)

	var problems []string
	for _, name := range corpusIn(t, dir) {
		file := abs(t, filepath.Join(dir, name+artExt))
		t.Run(name+artExt, func(t *testing.T) {
			found := jsParityOf(t, bin, js, file, env, mutate)
			problems = append(problems, found...)
			for _, p := range found {
				t.Error(p)
			}
		})
	}
	return problems
}

// jsToolchain is where node is and where the packages the generated module
// imports were found.
type jsToolchain struct {
	node    string
	vitest  string // the path to vitest's own entry point
	modules string // the node_modules the generated module is linked to
}

// jsParityEnv is what both backends need: the binary, the node toolchain, and an
// environment holding ARTEMIS_BASE_URL for a fixture server that lives as long as the
// test does.
//
// ARTEMIS_BASE_URL is how the corpus reaches the server -- `var base = env("ARTEMIS_BASE_URL")`
// is read by eval.Env.getenv under the interpreter and by process.env in the
// generated module, so one variable parameterises every backend and the corpus
// hard-codes no port.
func jsParityEnv(t *testing.T, modules ...string) (bin string, js jsToolchain, env []string) {
	t.Helper()
	js = nodeWith(t, append([]string{"vitest"}, modules...)...)
	bin = artemisBinary(t)
	srv := fixture.NewServer()
	t.Cleanup(srv.Close)
	return bin, js, append(os.Environ(), "ARTEMIS_BASE_URL="+srv.URL)
}

// jsParityOf runs one corpus file both ways and compares the two verdicts
// against each other and against what the file declares.
func jsParityOf(t *testing.T, bin string, js jsToolchain, file string, env []string, mutate func(string) string) []string {
	t.Helper()
	rows, undeclared := scenarios(t, file)
	if len(undeclared) > 0 {
		t.Fatalf("%s: no `# conformance: pass|fail` above %s",
			filepath.Base(file), strings.Join(undeclared, ", "))
	}
	interp := interpret(t, bin, file, env)
	ran := vitest(t, js, bin, file, env, mutate)
	for i := range rows {
		if _, ok := interp[rows[i].scenario]; !ok {
			t.Fatalf("%s: artemis run reported no scenario named %q",
				filepath.Base(file), rows[i].scenario)
		}
		if _, ok := ran[rows[i].jsTest]; !ok {
			t.Fatalf("%s: vitest collected no test named %q",
				filepath.Base(file), rows[i].jsTest)
		}
		rows[i].interp = interp[rows[i].scenario]
		rows[i].exported = ran[rows[i].jsTest]
	}
	return compare(filepath.Base(file), "vitest", rows)
}

// vitest generates the module with `artemis build` and runs it, returning each
// test's verdict.
//
// --reporter=junit rather than the exit code or the summary line, because the
// question is per scenario and a JUnit testcase is the only per-test answer
// vitest gives a program. The default reporter as well, because the junit one on
// its own writes nothing to the console -- and the console is where the assertion
// that failed is said out loud. --run so the watcher does not start, and
// --no-file-parallelism so one fixture server is not asked to serve several
// workers at once -- which is not wrong, only harder to read when it fails.
func vitest(t *testing.T, js jsToolchain, bin, file string, env []string, mutate func(string) string) map[string]outcome {
	t.Helper()
	dir := t.TempDir()

	build := exec.Command(bin, "build", "--lang=js", "-o", dir, file) //nolint:gosec // bin is the binary this test built
	build.Dir = dir
	build.Env = env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("artemis build %s: %v\n%s", file, err, out)
	}
	if mutate != nil {
		mutateJSModules(t, dir, mutate)
	}
	linkNodeModules(t, dir, js.modules)

	report := filepath.Join(dir, "vitest.xml")
	cmd := exec.Command(js.node, js.vitest, "run", //nolint:gosec // node came from exec.LookPath
		"--root", dir, "--no-file-parallelism",
		"--reporter=junit", "--outputFile="+report, "--reporter=default")
	cmd.Dir = dir
	cmd.Env = append(env, "CI=true")
	out, runErr := cmd.CombinedOutput()

	raw, err := os.ReadFile(report) //nolint:gosec // a path this test chose
	if err != nil {
		t.Fatalf("vitest wrote no report (%v): %v\n%s", err, runErr, out)
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
	failed := false
	for _, suite := range doc.Suites {
		for _, c := range suite.Cases {
			ok := len(c.Failures) == 0 && len(c.Errors) == 0
			failed = failed || !ok
			verdicts[c.Name] = verdict(ok)
		}
	}
	if failed {
		// vitest's own output, logged rather than dropped: the corpus declares
		// failures on purpose, so a failing test is not news -- but when the
		// comparison then reports a divergence, the assertion that failed is the
		// only thing that says which rule moved, and going and reproducing it by
		// hand is the expensive way to find out.
		t.Logf("vitest reported a failure for %s:\n%s", filepath.Base(file), out)
	}
	if len(verdicts) == 0 {
		// A module that does not import collects nothing, and a comparison
		// against nothing would look like a divergence with no cause. vitest's
		// own output is the only thing that says why.
		t.Fatalf("vitest collected no tests from the module generated for %s:\n%s", file, out)
	}
	return verdicts
}

// linkNodeModules makes the generated module's directory a package vitest can
// resolve its own imports from.
//
// A symlink rather than a copy, because the install is hundreds of megabytes and
// a corpus of four files would copy it four times. package.json marks the
// directory ESM, which is what lets node resolve the module's `import`
// statements if anything outside Vite ever looks at it.
func linkNodeModules(t *testing.T, dir, modules string) {
	t.Helper()
	if err := os.Symlink(modules, filepath.Join(dir, "node_modules")); err != nil {
		t.Fatalf("linking %s into %s: %v", modules, dir, err)
	}
	const pkg = "{ \"name\": \"artemis-parity\", \"private\": true, \"type\": \"module\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// mutateJSModules rewrites every generated module in dir, and fails if the
// mutation changed nothing -- a mutation test whose mutation did not apply is a
// test that passes for the wrong reason.
func mutateJSModules(t *testing.T, dir string, mutate func(string) string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.test.js"))
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

// nodeWith is the toolchain to run the generated modules with, or a skip naming
// what to install.
//
// Skipping rather than failing is how `make lint` treats a missing linter and how
// pythonWith treats a missing pytest: a toolchain nobody asked for must not stop
// someone building the binary. CI installs all of it, so the gate is real where
// it counts -- see .github/workflows/ci.yml.
//
// The packages are looked for in `npm root -g`, because a global install is the
// one place a test can find them without adding a package.json to this
// repository -- artemis is a Go module and ships no JavaScript.
func nodeWith(t *testing.T, packages ...string) jsToolchain {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is not installed; skipping the javascript execution-parity test (%v)", err)
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skipf("npm is not installed, so the packages cannot be found; "+
			"skipping the javascript execution-parity test (%v)", err)
	}
	out, err := exec.Command(npm, "root", "-g").Output() //nolint:gosec // npm came from exec.LookPath
	if err != nil {
		t.Skipf("`npm root -g` failed, so the packages cannot be found; "+
			"skipping the javascript execution-parity test (%v)", err)
	}
	modules := strings.TrimSpace(string(out))

	install := "npm install -g " + strings.Join(packages, " ")
	for _, pkg := range packages {
		if _, err := os.Stat(filepath.Join(modules, filepath.FromSlash(pkg))); err != nil {
			t.Skipf("the %s package is not installed in %s; skipping the javascript "+
				"execution-parity test (install it with `%s`)", pkg, modules, install)
		}
	}
	// vitest is run through its own entry point rather than through npx, so the
	// run cannot reach the network and cannot pick up a different version than
	// the one this just checked for.
	entry := filepath.Join(modules, "vitest", "vitest.mjs")
	if _, err := os.Stat(entry); err != nil {
		t.Skipf("%s is not there, so vitest cannot be run; skipping the javascript "+
			"execution-parity test (reinstall it with `%s`)", entry, install)
	}
	return jsToolchain{node: node, vitest: entry, modules: modules}
}

// The harness has to be able to report a divergence, or every green run of it
// means nothing. So one is produced on purpose: the generated module for api.art
// is rewritten to assert a status the fixture server does not return, which is
// what a lowering rule or an emitter rule that moved for the javascript backend
// alone would look like. The interpreter still passes the scenario, and compare
// must say the two disagree.
//
// The mutation is applied to the generated source rather than to the emitter,
// because the emitter has no seam to break at run time -- and this is the failure
// a reader cares about: the backends differ, whatever made them differ.
func TestJSParityDetectsADivergentBackend(t *testing.T) {
	bin, js, env := jsParityEnv(t)
	file := abs(t, filepath.Join(conformanceDir, "api"+artExt))

	problems := jsParityOf(t, bin, js, file, env, func(src string) string {
		return strings.ReplaceAll(src, "status === 200", "status === 500")
	})
	if len(problems) == 0 {
		t.Fatal("a mutated javascript module agreed with the interpreter; " +
			"this test cannot detect a divergent backend")
	}
	for _, p := range problems {
		if strings.Contains(p, "diverges") {
			return
		}
	}
	t.Errorf("the mutation was reported, but not as a divergence: %s", strings.Join(problems, "\n"))
}
