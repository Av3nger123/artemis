package cli

import (
	"flag"
	"fmt"
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
}

func TestGolden(t *testing.T) {
	for _, c := range goldenCases(t) {
		t.Run(c.name, func(t *testing.T) {
			out, runErr := runGolden(t, c)

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
func renderFixture(t *testing.T, c goldenCase, url string) (fixture, path string) {
	t.Helper()
	if !c.dir {
		fixture = filepath.Join("testdata", c.name+".yaml")
		path = filepath.Join(t.TempDir(), c.name+".yaml")
		writeRendered(t, fixture, path, url, true)
		return fixture, path
	}

	fixture = filepath.Join("testdata", c.name)
	path = filepath.Join(t.TempDir(), c.name)
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
			handler: func(w http.ResponseWriter, r *http.Request) {
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
			},
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
