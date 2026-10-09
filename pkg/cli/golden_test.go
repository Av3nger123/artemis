package cli

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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
// Each case names testdata/art/<name>.art -- the scenario a user would write --
// and testdata/art/<name>.golden, which holds every byte the command printed
// plus the error it exited with. The pair is the point: the .art file is
// readable as a scenario and the golden is readable as a report, so a change to
// either side of the runner shows up as a diff a person can judge.
//
// There was a second, parallel corpus of testdata/*.yaml fixtures and their
// goldens until ART-40, with TestGoldenParity arguing that a migrated scenario
// was the same run. The YAML files are still there, because they are
// `artemis migrate`'s corpus (migrate_test.go walks them); their run
// transcripts are not, because a parity test needs two runners and there is
// one.
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
	// stdoutOnly makes the transcript stdout alone, for a case whose stdout is
	// a document of its own -- the event stream -- that the console on stderr
	// would otherwise be interleaved with.
	stdoutOnly bool
}

// fixtureName is the testdata entry a case runs: its own name unless it borrows
// another's.
func (c goldenCase) fixtureName() string {
	if c.fixture != "" {
		return c.fixture
	}
	return c.name
}

// runGolden runs one case through the real root command and returns the
// scrubbed transcript: everything printed, then the error the command exited
// with.
func runGolden(t *testing.T, c goldenCase) (string, error) {
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

	fixture, path := renderFixture(t, c, url)

	var out strings.Builder
	RootCmd.SetArgs(append([]string{"run", path}, c.args...))
	RootCmd.SetOut(&out)
	if c.stdoutOnly {
		RootCmd.SetErr(io.Discard)
	} else {
		RootCmd.SetErr(&out)
	}
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
func renderFixture(t *testing.T, c goldenCase, url string) (fixture, path string) {
	t.Helper()
	name := c.fixtureName()
	if !c.dir {
		fixture = filepath.Join("testdata", artDir, name+artExt)
		path = filepath.Join(t.TempDir(), name+artExt)
		writeRendered(t, fixture, path, url, true)
		return fixture, path
	}

	fixture = filepath.Join("testdata", artDir, name)
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
	// The same two values in a compact document -- an --events line, which
	// json.Marshal writes with no space after the colon.
	jsonLineStartedAt  = regexp.MustCompile(`"started_at":"[^"]*"`)
	jsonLineDurationMS = regexp.MustCompile(`"duration_ms":[0-9.]+`)
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
	s = jsonLineStartedAt.ReplaceAllString(s, `"started_at":"1970-01-01T00:00:00Z"`)
	s = jsonLineDurationMS.ReplaceAllString(s, `"duration_ms":0`)
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

// goldenCases is the list of runs pinned to testdata/art. Keep a case's
// scenario in its fixture and only what the server does here.
//
// The names are the YAML corpus's, because each fixture is that scenario
// migrated and `artemis migrate`'s own goldens are keyed on them.
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
			// ART-54: a failed `expect` against a secret binding, with the
			// JSON document as well as the console summary, because both are
			// places an operand is written. The golden is the assertion that
			// `hunter2` appears in neither.
			name:    "secret_redacted",
			args:    []string{"--report", "json"},
			wantErr: true,
			handler: jsonHandler(http.StatusOK, `{"given": "not-the-password"}`),
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

// -- the corpus's own directory -------------------------------------------
//
// testdata/art holds one .art file (or folder) per goldenCase, with its own
// .golden beside it. It stayed a subdirectory when ART-40 removed the YAML
// corpus's goldens, because testdata/ still holds the .yaml files
// `artemis migrate` is tested on and a folder walk must not pick those up.
const artDir = "art"

// artCase is a case's departure from the YAML scenario it was migrated from,
// which for most of them is nothing at all. It is kept as the record of why
// each fixture reads the way it does; the parity test that used to enforce it
// went with the YAML runner.
type artCase struct {
	// noYAML is a case with no YAML original: it pins something the DSL has
	// and YAML did not.
	noYAML string
	// changed is a case whose outcome changes by design, with the reason.
	changed string
	// wantErr overrides the YAML case's, for a case whose exit changes.
	wantErr *bool
	// handler overrides the YAML case's, for a case the YAML corpus had no
	// server behaviour for.
	handler http.HandlerFunc
}

// artCases is every departure from the YAML corpus, in one table, each with the
// reason it departs. A case that is not here is the same scenario against the
// same server, and was held to the same outcome by TestGoldenParity until
// ART-40 deleted the runner it compared against.
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
			out, runErr := runGolden(t, c)
			checkGolden(t, c, out, runErr)
		})
	}
}

// checkGolden holds one case's exit to wantErr and its transcript to
// testdata/art/<name>.golden, or rewrites the golden under -update.
func checkGolden(t *testing.T, c goldenCase, out string, runErr error) {
	t.Helper()
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

// eventGoldenCases are four of the corpus's runs again, with --events ndjson
// and nothing else: a file that passes, a file that fails, a file that does not
// compile, and report_json's folder, which has one of each in one run. Each
// borrows its fixture and server from the case it repeats, so the stream reads
// against that case's console golden and, for report_json, its document.
func eventGoldenCases(t *testing.T) []goldenCase {
	t.Helper()
	byName := map[string]goldenCase{}
	for _, c := range artGoldenCases(t) {
		byName[c.name] = c
	}
	var cases []goldenCase
	// secret_redacted: a step-end carries operands, so the stream is one more
	// place a credential must not appear (ART-54).
	for _, name := range []string{"pass", "fail", "template_error", "report_json", "secret_redacted"} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("no golden case named %s to repeat with --events", name)
		}
		c.name = "events_" + name
		c.fixture = name
		// Replaced, not appended: report_json's own --report json would be
		// a second document on stdout, which --events refuses.
		c.args = []string{"--events", "ndjson"}
		c.stdoutOnly = true
		cases = append(cases, c)
	}
	return cases
}

// TestGoldenEvents pins the whole event stream of a run, one JSON object per
// line, plus the error the command exited with. stderr -- the console, the
// diagnostics -- is left out: it is what TestGoldenArt pins already.
func TestGoldenEvents(t *testing.T) {
	for _, c := range eventGoldenCases(t) {
		t.Run(c.name, func(t *testing.T) {
			out, runErr := runGolden(t, c)
			checkGolden(t, c, out, runErr)
		})
	}
}
