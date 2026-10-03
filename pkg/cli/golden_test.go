package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"artemis/pkg/dsl/print"
)

// -update rewrites the golden files from what the runner actually prints:
//
//	go test ./pkg/cli -run TestGolden -update
//
// or `make golden`. Read the resulting diff before committing it -- a golden
// file that is regenerated without being read asserts nothing.
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// serverPlaceholder is what a fixture writes where the test server's address
// goes. The committed fixture therefore has no port in it, and a fixture whose
// placeholder is never substituted fails loudly rather than quietly calling
// somewhere real.
const serverPlaceholder = "%SERVER%"

// durSentinel is what a duration is replaced with before comparison.
const durSentinel = "<dur>"

// goldenCase is one whole run of artemis pinned to a file.
//
// Each case names testdata/<name>.yaml -- the scenario a user would write -- and
// testdata/<name>.golden, which holds every byte the command printed plus the
// error it exited with. The pair is the point: the YAML is readable as a
// scenario and the golden is readable as a report, so a change to either side
// of the runner shows up as a diff a person can judge.
type goldenCase struct {
	name string
	// dir makes the fixture a folder, testdata/<name>/, run with
	// `artemis run <folder>` -- every scenario in it, as one run.
	dir bool
	// handler answers the scenario's requests. Nil for a case that fails
	// before anything is sent.
	handler http.HandlerFunc
	// wantErr is whether the run must fail the process (ART-2).
	wantErr bool
	// noSleep stubs out the retry sleep, so a case can ask for a delay
	// without the suite waiting it out.
	noSleep bool
	// args are extra flags appended after the path, for a case that pins what
	// a flag does to a whole run.
	args []string
	// fixture is the testdata name to run, when it is not the case's own --
	// two cases that pin two reports of the same run share one fixture, so the
	// documents are directly comparable.
	fixture string
}

// fixtureName is the testdata entry a case runs: its own name unless it borrows
// another's.
func (c goldenCase) fixtureName() string {
	if c.fixture != "" {
		return c.fixture
	}
	return c.name
}

func TestGolden(t *testing.T) {
	for _, c := range goldenCases(t) {
		t.Run(c.name, func(t *testing.T) {
			out, runErr := runGoldenIn(t, "", c)

			if c.wantErr && runErr == nil {
				t.Errorf("Execute() = nil, want an error so the process exits non-zero:\n%s", out)
			}
			if !c.wantErr && runErr != nil {
				t.Errorf("Execute() = %v, want nil:\n%s", runErr, out)
			}

			path := filepath.Join("testdata", c.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}
				t.Logf("updated %s", path)
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v (run `make golden` to create it)", path, err)
			}
			if got := out; got != string(want) {
				t.Errorf("output does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
			}
		})
	}
}

// runGoldenIn runs one case through the real root command and returns the
// scrubbed transcript: everything printed, then the error the command exited
// with.
//
// dir is the corpus the fixture lives in, relative to testdata: "" for the
// YAML fixtures and artDir for the DSL ones. It is a parameter rather than two
// functions so that both corpora go through the same root command, the same
// server, the same substitution and the same scrubbing -- which is what makes
// the two transcripts comparable at all.
func runGoldenIn(t *testing.T, dir string, c goldenCase) (string, error) {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)

	if c.noSleep {
		orig := sleep
		sleep = func(time.Duration) {}
		t.Cleanup(func() { sleep = orig })
	}

	// A case with no handler never dials; port 1 is still a real address, so a
	// case that does dial fails fast instead of hanging.
	url := "http://127.0.0.1:1"
	if c.handler != nil {
		srv := httptest.NewServer(c.handler)
		t.Cleanup(srv.Close)
		url = srv.URL
	}

	fixture, path := renderFixture(t, dir, c, url)

	var out strings.Builder
	RootCmd.SetArgs(append([]string{"run", path}, c.args...))
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&out)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })
	runErr := RootCmd.Execute()

	transcript := out.String()
	if runErr != nil {
		transcript += "\n--- exit error ---\n" + runErr.Error() + "\n"
	}
	return scrub(transcript, path, fixture, url), runErr
}

// renderFixture copies the case's fixture -- one file, or a whole folder for a
// dir case -- into a temp location with the test server's address substituted
// for %SERVER%, and returns the fixture path the transcript should name and the
// temp path to run.
//
// The copy is needed because the server's port is only known now; the temp path
// is scrubbed back to the fixture's, so the golden file reads as a run of the
// committed suite.
func renderFixture(t *testing.T, dir string, c goldenCase, url string) (fixture, path string) {
	t.Helper()
	name := c.fixtureName()
	ext := ".yaml"
	if dir == artDir {
		ext = artExt
	}
	if !c.dir {
		fixture = filepath.Join("testdata", dir, name+ext)
		path = filepath.Join(t.TempDir(), name+ext)
		writeRendered(t, fixture, path, url, true)
		return fixture, path
	}

	fixture = filepath.Join("testdata", dir, name)
	path = filepath.Join(t.TempDir(), name)
	substituted := false
	err := filepath.WalkDir(fixture, func(src string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixture, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(path, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750)
		}
		substituted = writeRendered(t, src, dst, url, false) || substituted
		return nil
	})
	if err != nil {
		t.Fatalf("copying %s: %v", fixture, err)
	}
	if !substituted {
		t.Fatalf("no file under %s contains %s -- a fixture must not hardcode an address", fixture, serverPlaceholder)
	}
	return fixture, path
}

// writeRendered copies src to dst with %SERVER% replaced by url, and reports
// whether the placeholder was there. With must set, a file without it is a
// fixture that hardcodes an address, and that fails the test.
func writeRendered(t *testing.T, src, dst, url string, must bool) bool {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	found := strings.Contains(string(raw), serverPlaceholder)
	if must && !found {
		t.Fatalf("%s does not contain %s -- a fixture must not hardcode an address", src, serverPlaceholder)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(strings.ReplaceAll(string(raw), serverPlaceholder, url)), 0o600); err != nil {
		t.Fatal(err)
	}
	return found
}

// durPattern matches what report.dur prints: a Go duration rounded to
// milliseconds, or the "<1ms" it uses for something faster than it can show.
const durPattern = `<1ms|(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ns|µs|ms|s)`

// spacedDur matches a duration together with the whitespace in front of it, so
// the replacement can keep the column exactly as wide as it was.
var spacedDur = regexp.MustCompile(` +(?:` + durPattern + `)`)

// jsonStartedAt and jsonDurationMS match the two values in a --report json
// document that change between runs. They are replaced with fixed values rather
// than a sentinel word, so a report golden stays a parseable JSON document
// anyone can pipe into jq.
var (
	jsonStartedAt  = regexp.MustCompile(`"started_at": "[^"]*"`)
	jsonDurationMS = regexp.MustCompile(`"duration_ms": [0-9.]+`)
)

// xmlTimestamp and xmlTime match the two values in a --report junit document
// that change between runs, replaced for the same reason as the JSON ones: a
// report golden stays a document a reader can paste into a CI reporter.
var (
	xmlTimestamp = regexp.MustCompile(`timestamp="[^"]*"`)
	xmlTime      = regexp.MustCompile(`time="[0-9.]+"`)
)

// scrub replaces everything in a transcript that changes between runs: the temp
// scenario path, the test server's address, every duration, and the timings
// inside a JSON report.
//
// Durations are replaced in place, keeping the field the same total width --
// from the end of the preceding text to the end of the duration -- which is
// fixed by report's format string even though the duration's own length is not.
// What a duration actually reads as, and the exact column layout, are pinned by
// pkg/report/console_test.go against a hand-built result tree; a golden file is
// here for the shape of a whole run.
func scrub(s, tempPath, fixture, url string) string {
	s = strings.ReplaceAll(s, tempPath, fixture)
	s = strings.ReplaceAll(s, url, "http://127.0.0.1:PORT")
	s = jsonStartedAt.ReplaceAllString(s, `"started_at": "1970-01-01T00:00:00Z"`)
	s = jsonDurationMS.ReplaceAllString(s, `"duration_ms": 0`)
	s = xmlTimestamp.ReplaceAllString(s, `timestamp="1970-01-01T00:00:00Z"`)
	s = xmlTime.ReplaceAllString(s, `time="0.000"`)
	return spacedDur.ReplaceAllStringFunc(s, func(m string) string {
		pad := len(m) - len(durSentinel)
		if pad < 1 {
			pad = 1
		}
		return strings.Repeat(" ", pad) + durSentinel
	})
}

// jsonHandler answers every request with one status and body.
func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// goldenCases is the list of runs pinned to testdata. Keep a case's scenario in
// its fixture and only what the server does here.
func goldenCases(t *testing.T) []goldenCase {
	t.Helper()
	return []goldenCase{
		{
			// A whole passing run: a capture out of the first response
			// templated into the second step's URL.
			name: "pass",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/items":
					w.WriteHeader(http.StatusCreated)
					fmt.Fprint(w, `{"id": 42, "name": "widget"}`)
				case "/items/42":
					fmt.Fprint(w, `{"id": 42, "name": "widget", "tags": ["a", "b"]}`)
				default:
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprintf(w, `{"error": "no route for %s"}`, r.URL.Path)
				}
			},
		},
		{
			// One body assertion is wrong: the step fails, the run fails,
			// and the assertion is printed under the step that made it.
			name:    "fail",
			handler: jsonHandler(http.StatusOK, `{"status": "pending", "count": 3}`),
			wantErr: true,
		},
		{
			// The status code is not what the scenario asked for, so the body
			// checks are never made -- one failed assertion, not three.
			name:    "wrong_status",
			handler: jsonHandler(http.StatusInternalServerError, `{"error": "boom"}`),
			wantErr: true,
		},
		{
			// A variable nobody declared: the step errors before a request is
			// sent (ART-6). No real network failure is pinned here -- the
			// runtime's wording for a refused dial is not ours to freeze.
			name:    "template_error",
			wantErr: true,
		},
		{
			// An unparseable retry delay is the scenario's mistake, and it
			// stops the step before anything is sent (ART-5).
			name:    "bad_retry",
			wantErr: true,
		},
		{
			// The request is fine and the body assertion passes; neither
			// capture can be read. Each is an errored assertion under the
			// step, so the run fails with a reason per capture instead of
			// passing on variables nothing ever set.
			name:    "bad_capture",
			handler: jsonHandler(http.StatusOK, `{"token": "abc"}`),
			wantErr: true,
		},
		{
			// A capture off a body that is not JSON: the regex reads the raw
			// text and the value is templated into the next step's URL
			// (ART-18).
			name: "regex_capture",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/redirect":
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, "moved to /items/42\n")
				case "/items/42":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id": 42}`)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
		},
		{
			// A whole folder: discovery is recursive and in path order, the
			// file that will not load is one errored scenario naming its
			// path, and the files after it still run -- all of it one run
			// with one summary and one exit code (ART-9).
			name:    "suite",
			dir:     true,
			wantErr: true,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/login":
					fmt.Fprint(w, `{"token": "t0ken"}`)
				case "/items":
					fmt.Fprint(w, `{"items": []}`)
				default:
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprintf(w, `{"error": "no route for %s"}`, r.URL.Path)
				}
			},
		},
		{
			// The whole --report json document for a run with something of
			// every kind in it: a scenario that passed, one with a failed
			// assertion and an errored step, and a file that would not load
			// and so has no steps at all (ART-10). The console report is on
			// stderr here, which the transcript merges back in front of the
			// document.
			name:    "report_json",
			dir:     true,
			args:    []string{"--report", "json"},
			wantErr: true,
			handler: reportFixtureHandler(),
		},
		{
			// The same run as report_json, as JUnit XML: a scenario is a
			// testsuite and a step is a testcase, a failed step carries
			// <failure> and an errored one <error>, and the file that would
			// not load is a suite with one synthetic errored case so the load
			// failure is visible in a CI UI (ART-11). Sharing report_json's
			// fixture makes the two documents comparable.
			name:    "report_junit",
			fixture: "report_json",
			dir:     true,
			args:    []string{"--report", "junit"},
			wantErr: true,
			handler: reportFixtureHandler(),
		},
		{
			// Two failures then a pass: the step passes, and its line says how
			// many attempts it took.
			name:    "retried",
			noSleep: true,
			handler: func() http.HandlerFunc {
				var calls atomic.Int32
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if calls.Add(1) < 3 {
						w.WriteHeader(http.StatusServiceUnavailable)
						fmt.Fprint(w, `{"status": "starting"}`)
						return
					}
					fmt.Fprint(w, `{"status": "ok"}`)
				}
			}(),
		},
	}
}

// reportFixtureHandler answers the testdata/report_json suite, which both report
// goldens run: one scenario that passes, one with a failed assertion and an
// errored step, and a file that will not load.
func reportFixtureHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status": "ok", "build": "1a2b3c"}`)
		case "/items":
			fmt.Fprint(w, `{"total": 0, "items": []}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"error": "no route for %s"}`, r.URL.Path)
		}
	}
}

// -- the DSL corpus -------------------------------------------------------
//
// testdata/art holds the same runs written in the DSL: one .art file (or
// folder) per goldenCase, with its own .golden beside it. The tree is parallel
// rather than mixed in with the YAML fixtures because a folder walk now picks
// up both extensions, so a suite holding `01_login.yaml` and `01_login.art`
// would run each scenario twice.
//
// Two tests use it, and the division is the point. TestGoldenArt pins what the
// DSL prints, which is a diff a person reads. TestGoldenParity is the argument
// that it is the same run as the YAML one -- see parityFields.
const artDir = "art"

// artCase describes how a goldenCase differs in the DSL, which for most of them
// is not at all.
type artCase struct {
	// noYAML is a case with no YAML fixture: it pins something the DSL has and
	// YAML does not, so there is nothing to compare it with.
	noYAML string
	// changed is a case whose outcome changes by design, with the reason. It
	// still has a golden -- what it prints is pinned -- but no parity claim.
	changed string
	// wantErr overrides the YAML case's, for a case whose exit changes.
	wantErr *bool
	// handler overrides the YAML case's, for a case the YAML corpus has no
	// server behaviour for.
	handler http.HandlerFunc
}

// artCases is every departure from the YAML corpus, in one table, each with the
// reason it departs. A case that is not here runs the same scenario against the
// same server and must produce the same outcome.
func artCases() map[string]artCase {
	yes := true
	return map[string]artCase{
		// The two compile errors. In YAML both of these are run-time step
		// errors; the checker resolves names and parses durations, so in the
		// DSL the file never runs. The design calls for the first and names
		// "bad duration" in the same compile-error class as the second.
		"template_error": {changed: "an unresolvable placeholder is a compile error"},
		"bad_retry":      {changed: "a delay that is not a duration is a compile error"},

		// Not a departure at all, recorded here because the design document
		// says it is one: it describes bad_capture as pinning "an empty
		// capture path", which was already a YAML load error. What the fixture
		// pins is a path that resolves to nothing and a regex that matches
		// nothing, and a response's shape is not something a checker can know,
		// so both stay run-time errored captures and the case is a parity case.

		// The DSL-only fixtures.
		"status_and_body": {
			noYAML:  "a wrong status no longer suppresses the expects after it",
			wantErr: &yes,
			handler: jsonHandler(http.StatusInternalServerError, `{"error": "boom"}`),
		},
		"terminal": {
			noYAML:  "api and terminal in one scenario, and two scenarios in one file",
			wantErr: &yes,
			handler: jsonHandler(http.StatusOK, `{"items": []}`),
		},
	}
}

// artGoldenCases is the YAML corpus plus the DSL-only cases, each adjusted by
// artCases.
func artGoldenCases(t *testing.T) []goldenCase {
	t.Helper()
	over := artCases()
	cases := goldenCases(t)
	for i := range cases {
		a := over[cases[i].name]
		if a.wantErr != nil {
			cases[i].wantErr = *a.wantErr
		}
		if a.handler != nil {
			cases[i].handler = a.handler
		}
	}
	// In the order they are declared in artCases, which map iteration does not
	// give, so the DSL-only ones are listed here explicitly.
	for _, name := range []string{"status_and_body", "terminal"} {
		a := over[name]
		cases = append(cases, goldenCase{
			name:    name,
			handler: a.handler,
			wantErr: a.wantErr != nil && *a.wantErr,
		})
	}
	return cases
}

// TestGoldenArt is TestGolden over the DSL corpus: the same runs, the same
// server behaviour, and a golden per case holding every byte the command
// printed plus the error it exited with.
func TestGoldenArt(t *testing.T) {
	for _, c := range artGoldenCases(t) {
		t.Run(c.name, func(t *testing.T) {
			out, runErr := runGoldenIn(t, artDir, c)

			if c.wantErr && runErr == nil {
				t.Errorf("Execute() = nil, want an error so the process exits non-zero:\n%s", out)
			}
			if !c.wantErr && runErr != nil {
				t.Errorf("Execute() = %v, want nil:\n%s", runErr, out)
			}

			path := filepath.Join("testdata", artDir, c.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}
				t.Logf("updated %s", path)
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v (run `make golden` to create it)", path, err)
			}
			if got := out; got != string(want) {
				t.Errorf("output does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
			}
		})
	}
}

// Every .art file in the corpus is already in canonical form, so the fixtures
// read as the formatter would write them and a reviewer never has to wonder
// whether a layout is deliberate. A file that does not compile is skipped,
// which is `artemis fmt`'s own rule: it does not format a file it has errors
// for, because rewriting a broken file is how a formatter loses someone's work.
func TestGoldenArtFixturesAreCanonical(t *testing.T) {
	root := filepath.Join("testdata", artDir)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".art") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree, _, bag := frontEnd(path, string(src))
		if bag.HasErrors() {
			return nil
		}
		if got := print.Canonical(tree); got != string(src) {
			t.Errorf("%s is not in canonical form; run `artemis fmt -w %s`\n--- want ---\n%s\n--- got ---\n%s",
				path, path, src, got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

// -- behaviour parity -----------------------------------------------------
//
// This is the argument the whole issue is for: that a scenario migrated to the
// DSL is the *same run*.
//
// It is made over the --report json documents rather than over the console
// transcripts, because the transcripts cannot be byte-identical and the reason
// they cannot is uninteresting. A heading names its fixture's path, a failure
// block names its line, and an assertion's subject reads `body $.status equals`
// in YAML and `expect body.status ==` in the DSL. Redacting those out of a text
// diff would mean a regex that has to find the operands inside
// `<subject> <expected>, got <actual>` -- and the operands are the part worth
// pinning. The JSON report is the same result tree with every field named, so
// the comparison can say exactly what the two languages may disagree about.
//
// What survives redaction, and therefore has to match: schema_version, the run
// status and verdict, all nine tallies, every scenario's name and status, every
// step's name, status and attempts, every assertion's expected, actual and
// status, and the failures array's length, order, scenario, step, status,
// expected and actual.

// parityRedacted are the document keys the two formats may differ in, each with
// the reason. Every other key must match exactly, at every level.
//
// A key is redacted wherever it appears, which is what makes the list short: an
// assertion's `path` and the same assertion's `path` inside the flat failures
// array are one rule, not two.
var parityRedacted = map[string]string{
	"started_at":  "when the run happened",
	"duration_ms": "how long it took",
	"file":        "the fixture's own path, and its extension",
	"line":        "the line of a different file",
	"kind":        `"body" or "status_code" against "expect": the YAML reader classified a check, the DSL has one statement for all of them`,
	"path":        "`$.status` against `body.status`: the subject in each language's own syntax",
	"operator":    "`equals` against `==`, `lte` against `<=`: the same comparison, spelled as the author wrote it",
}

// parityErroredOperands is why an errored assertion's two operands are redacted
// as well.
//
// An errored assertion made no comparison -- an absent path, a regex that
// matched nothing -- so neither operand describes anything that happened, and
// the two readers fill them differently: pkg/shared/capture echoes the JSON path
// into `expected`, and pkg/dsl/lower deliberately leaves both null, on the
// grounds that a capture that compared nothing should not look as though it
// did. The subject is already redacted under `path` and `operator` for the same
// reason, so this is the same exception reaching one field further.
//
// It is narrow on purpose: `expected` and `actual` stay pinned on every
// assertion that passed or failed, which is every assertion that compared
// anything.
const parityErroredOperands = "an errored assertion compared nothing"

// parityErrorSentinel is what a non-empty error is replaced with. The text is
// format-specific -- a YAML decoder's complaint against a diagnostic, and
// `path "$.session.token": nothing is at that path` against the evaluator's
// absent-path wording -- but *that* it errored, and that it gave a reason, is
// pinned.
const parityErrorSentinel = "<error>"

// TestGoldenParity holds every migrated fixture to the same run as its YAML
// original.
func TestGoldenParity(t *testing.T) {
	over := artCases()
	for _, c := range artGoldenCases(t) {
		a := over[c.name]
		switch {
		case a.noYAML != "":
			t.Run(c.name, func(t *testing.T) { t.Skipf("no YAML fixture: %s", a.noYAML) })
			continue
		case a.changed != "":
			t.Run(c.name, func(t *testing.T) { t.Skipf("changes by design: %s", a.changed) })
			continue
		case c.fixture != "":
			// Two cases pinning two reports of one run. The run is compared
			// once, under the case that owns the fixture.
			continue
		}
		name := c.name
		t.Run(name, func(t *testing.T) {
			// A case per run, rebuilt: a handler may be stateful -- `retried`
			// counts the calls it has answered -- so two runs sharing one
			// handler is two runs sharing a counter, and the second would see a
			// service that was already up.
			yaml := runParityReport(t, "", namedCase(t, name))
			art := runParityReport(t, artDir, namedCase(t, name))
			if path, ok := firstDiff(nil, yaml, art); !ok {
				t.Errorf("the .art run is not the same run as the YAML one, at %s\n--- yaml ---\n%s\n--- art ---\n%s",
					path, reindent(yaml), reindent(art))
			}
		})
	}
}

// namedCase rebuilds one case from the table, with a handler nothing else has
// called yet.
func namedCase(t *testing.T, name string) goldenCase {
	t.Helper()
	for _, c := range artGoldenCases(t) {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no golden case named %q", name)
	return goldenCase{}
}

// runParityReport runs one case and returns its --report json document with the
// redacted keys removed.
//
// Always --report json, whatever the case's own args asked for: the document is
// what is being compared, and a case that pins a *second* format pins nothing
// extra about the run.
func runParityReport(t *testing.T, dir string, c goldenCase) any {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)

	if c.noSleep {
		orig := sleep
		sleep = func(time.Duration) {}
		t.Cleanup(func() { sleep = orig })
	}

	url := "http://127.0.0.1:1"
	if c.handler != nil {
		srv := httptest.NewServer(c.handler)
		t.Cleanup(srv.Close)
		url = srv.URL
	}
	_, path := renderFixture(t, dir, c, url)

	// stdout carries exactly the document, which is the invariant reportRun
	// moves the console report to stderr for. Here it is what makes the
	// document parseable without scrubbing a transcript apart.
	var doc, discard strings.Builder
	RootCmd.SetArgs(append([]string{"run", path, "--report", "json"}, withoutReport(c.args)...))
	RootCmd.SetOut(&doc)
	RootCmd.SetErr(&discard)
	t.Cleanup(func() { RootCmd.SetOut(os.Stderr); RootCmd.SetErr(os.Stderr) })
	_ = RootCmd.Execute()

	var parsed any
	if err := json.Unmarshal([]byte(doc.String()), &parsed); err != nil {
		t.Fatalf("the %s run did not write a JSON report: %v\n%s", or(dir, "yaml"), err, doc.String())
	}
	return redact(parsed)
}

// withoutReport drops a case's own --report flag and its value, so the parity
// run writes one document and the case's other flags still apply.
func withoutReport(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--report" {
			i++ // and its value
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// redact removes every parityRedacted key and reduces every error to
// empty-or-not, at every level of the document.
func redact(v any) any {
	switch v := v.(type) {
	case map[string]any:
		// See parityErroredOperands. The two operands are dropped only on a map
		// that has them and that says it errored, which is an assertion or a
		// failures entry and never a run, a scenario or a step.
		errored := v["status"] == "error"
		out := make(map[string]any, len(v))
		for key, value := range v {
			if _, skip := parityRedacted[key]; skip {
				continue
			}
			if errored && (key == "expected" || key == "actual") {
				continue
			}
			if key == "error" {
				if text, ok := value.(string); ok && text != "" {
					out[key] = parityErrorSentinel
					continue
				}
			}
			out[key] = redact(value)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = redact(v[i])
		}
		return out
	default:
		return v
	}
}

// firstDiff reports whether two redacted documents are equal, and where they
// first differ when they are not.
//
// The path rather than a whole-document diff, because the documents are long
// and the one field that moved is the whole finding: "counts.steps.errored: 0
// against 1" is a sentence a reader acts on.
func firstDiff(path []string, want, got any) (string, bool) {
	at := strings.Join(path, ".")
	if at == "" {
		at = "the document"
	}
	switch want := want.(type) {
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: an object against %T", at, got), false
		}
		for _, key := range sortedKeys(want, gotMap) {
			wantValue, inWant := want[key]
			gotValue, inGot := gotMap[key]
			if inWant != inGot {
				return fmt.Sprintf("%s.%s: present in one document only", at, key), false
			}
			if where, ok := firstDiff(append(path, key), wantValue, gotValue); !ok {
				return where, false
			}
		}
		return "", true
	case []any:
		gotSlice, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: an array against %T", at, got), false
		}
		if len(want) != len(gotSlice) {
			return fmt.Sprintf("%s: %d entries against %d", at, len(want), len(gotSlice)), false
		}
		for i := range want {
			if where, ok := firstDiff(append(path, strconv.Itoa(i)), want[i], gotSlice[i]); !ok {
				return where, false
			}
		}
		return "", true
	default:
		if !reflect.DeepEqual(want, got) {
			return fmt.Sprintf("%s: %v against %v", at, render(want), render(got)), false
		}
		return "", true
	}
}

// sortedKeys is the union of two maps' keys, sorted, so a mismatch is reported
// at the same key on every run.
func sortedKeys(a, b map[string]any) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for key := range m {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// render is one value as it appears in the document, for a mismatch message.
func render(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// reindent is a redacted document back as indented JSON, for the mismatch
// message's two halves.
func reindent(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
