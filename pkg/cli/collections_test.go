package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runCLI runs the real root command with args and returns stdout and stderr
// separately, with every command's flags back at their defaults first.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	initOnce.Do(Init)
	resetRunFlags(t)
	resetFrontEndFlags(t)

	var out, errOut bytes.Buffer
	RootCmd.SetArgs(args)
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		RootCmd.SetOut(os.Stderr)
		RootCmd.SetErr(os.Stderr)
		RootCmd.SetArgs(nil)
	})
	runErr := RootCmd.Execute()
	return out.String(), errOut.String(), runErr
}

// authServer is the service checkout.art talks to: a token for the right
// password, 401 for any other, and orders for the token it hands out.
func authServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Password != "s3cret" {
				// Still a token-shaped body: `use auth.login as bad` drops
				// the request's expects, not its capture, so bad_token has
				// to be readable off a refusal too.
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"data":{"access_token":"refused"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"access_token":"t"}}`))
		case "/orders":
			if r.Header.Get("Authorization") != "Bearer t" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunAScenarioThatUsesACollection(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	out, stderr, err := runCLI(t, "run", "testdata/collections/checkout.art", "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	for _, want := range []string{`"auth.login"`, `"bad"`, `"orders"`} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing step %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") || strings.Contains(stderr, "s3cret") {
		t.Fatal("a secret parameter leaked into the report")
	}
}

func TestBuildAScenarioThatUsesACollection(t *testing.T) {
	out, stderr, err := runCLI(t, "build", "--lang=python", "testdata/collections/checkout.art")
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "auth.login") || !strings.Contains(out, "/orders") {
		t.Fatalf("build did not generate the expanded steps:\n%s", out)
	}
}

func TestParseAScenarioThatUsesACollection(t *testing.T) {
	out, stderr, err := runCLI(t, "parse", "-f", "testdata/collections/checkout.art")
	if err != nil {
		t.Fatalf("parse failed: %v\n%s", err, stderr)
	}
	if out != "testdata/collections/checkout.art: ok\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestBrokenCollectionReportedOnceAgainstTheCollection(t *testing.T) {
	_, stderr, err := runCLI(t, "parse", "-f", "testdata/collections/broken.art")
	if err == nil {
		t.Fatal("want a non-zero exit")
	}
	// The error is in the collection, found by its standalone check, so it is
	// reported once and has no use chain -- even though two scenarios use it.
	if strings.Count(stderr, "broken_coll.art:") != 1 || strings.Contains(stderr, "used from") {
		t.Fatalf("got\n%s", stderr)
	}
	if strings.Count(stderr, "statu ") != 1 {
		t.Fatalf("got\n%s", stderr)
	}
}

func TestFmtDoesNotExpand(t *testing.T) {
	out, stderr, err := runCLI(t, "fmt", "testdata/collections/checkout.art")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(out, "use auth.login") || strings.Contains(out, `step "auth.login"`) {
		t.Fatalf("fmt must print the file as written:\n%s", out)
	}
}

// A failure in a step a use brought in names the collection file its line was
// written in -- on the console, in the JSON report's failures, and on the
// step and assertion -- not the scenario file with a line from another file.
func TestAFailureInACollectionNamesTheCollectionFile(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	stdout, stderr, err := runCLI(t, "run", "testdata/collections/failing.art", "--report", "json")
	if err == nil {
		t.Fatal("want the run to fail")
	}
	if !strings.Contains(stderr, "testdata/collections/auth.art:6") {
		t.Errorf("the console does not name auth.art:6:\n%s", stderr)
	}
	if strings.Contains(stderr, "failing.art:6") {
		t.Errorf("the console names the scenario file with the collection's line:\n%s", stderr)
	}
	var doc struct {
		Failures []struct {
			File string `json:"file"`
			Line int    `json:"line"`
		} `json:"failures"`
		Scenarios []struct {
			File  string `json:"file"`
			Steps []struct {
				File       string `json:"file"`
				Line       int    `json:"line"`
				Assertions []struct {
					File string `json:"file"`
					Line int    `json:"line"`
				} `json:"assertions"`
			} `json:"steps"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if len(doc.Failures) != 1 || doc.Failures[0].File != "testdata/collections/auth.art" || doc.Failures[0].Line != 6 {
		t.Errorf("failures = %+v", doc.Failures)
	}
	step := doc.Scenarios[0].Steps[0]
	if step.File != "testdata/collections/auth.art" || step.Line != 2 {
		t.Errorf("step = %+v", step)
	}
	if a := step.Assertions[0]; a.File != "testdata/collections/auth.art" || a.Line != 6 {
		t.Errorf("assertion = %+v", a)
	}
	if !strings.Contains(stdout, `"file": "testdata/collections/auth.art"`) &&
		!strings.Contains(stdout, `"file":"testdata/collections/auth.art"`) {
		t.Errorf("no file key on the step:\n%s", stdout)
	}
}

// A step written in the scenario itself carries no file key: it would only
// repeat the scenario's.
func TestAStepWrittenInTheScenarioHasNoFileKey(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	stdout, _, err := runCLI(t, "run", "testdata/collections/checkout.art", "--report", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scenarios []struct {
			Steps []map[string]any `json:"steps"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	for _, st := range doc.Scenarios[0].Steps {
		_, has := st["file"]
		if want := st["name"] != "orders"; has != want {
			t.Errorf("step %v: file key present = %v, want %v", st["name"], has, want)
		}
	}
}

// A file of only collections runs nothing and passes, so a folder run is not
// upset by a collections/ directory beside the scenarios.
func TestRunAFolderWithACollectionOnlyFile(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	dir := writeTree(t, t.TempDir(), map[string]string{
		"auth.art":     mustRead(t, "testdata/collections/auth.art"),
		"checkout.art": mustRead(t, "testdata/collections/checkout.art"),
	})
	stdout, stderr, err := runCLI(t, "run", dir, "--report", "json")
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, stdout, stderr)
	}
	var doc struct {
		Scenarios []struct {
			Name string `json:"name"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Scenarios) != 1 || doc.Scenarios[0].Name != "checkout" {
		t.Fatalf("scenarios = %+v", doc.Scenarios)
	}
}

// A hoisted secret var is a secret like any other: its value is in neither
// the event stream nor a trace.
func TestAHoistedSecretIsInNoEventOrTrace(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	traces := t.TempDir()
	stdout, stderr, err := runCLI(t, "run", "testdata/collections/checkout.art", "--events", "ndjson", "--trace", traces)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "step-end") {
		t.Fatalf("no event stream:\n%s", stdout)
	}
	// The event stream's step-end carries the step's file like the report
	// does, for a step a use brought in.
	if !strings.Contains(stdout, `"file":"testdata/collections/auth.art"`) {
		t.Errorf("no step-end names auth.art:\n%s", stdout)
	}
	var all strings.Builder
	all.WriteString(stdout)
	all.WriteString(stderr)
	entries, err := os.ReadDir(traces)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no traces written: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(traces, e.Name())) //nolint:gosec // a dir this test made
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	for _, secret := range []string{"s3cret", `"wrong"`} {
		if strings.Contains(all.String(), secret) {
			t.Errorf("%s leaked into the events or a trace", secret)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // a testdata path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Two uses of one request with secret arguments each send their own value:
// with as, each hoists its own var. Without as both hoisted the same name and
// the last value was sent twice, which is now duplicate-binding.
func TestTwoUsesOfASecretRequestSendTheirOwnPasswords(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body.Password)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if body.Password != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_, _ = w.Write([]byte(`{"data":{"access_token":"t"}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("API_URL", srv.URL)

	scenario := func(real, bad string) string {
		return "import \"auth.art\"\n\nscenario \"s\" {\n  var url = env(\"API_URL\")\n" +
			"  use auth.login" + real + " { user = \"alice\", password = \"s3cret\", base = url }\n" +
			"  use auth.login" + bad + " { user = \"alice\", password = \"wrong\", base = url, drop captures, drop expects, expect status == 401 }\n}\n"
	}
	dir := writeTree(t, t.TempDir(), map[string]string{
		"auth.art":  mustRead(t, "testdata/collections/auth.art"),
		"two.art":   scenario(" as real", " as bad"),
		"clash.art": scenario("", ""),
	})

	stdout, stderr, err := runCLI(t, "run", filepath.Join(dir, "two.art"), "--report", "json")
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, stdout, stderr)
	}
	if strings.Join(sent, ",") != "s3cret,wrong" {
		t.Fatalf("passwords sent = %v, want each use's own", sent)
	}

	sent = nil
	_, stderr, err = runCLI(t, "run", filepath.Join(dir, "clash.art"))
	if err == nil || !strings.Contains(stderr, `"login_password" is already bound`) {
		t.Fatalf("want duplicate-binding, got err %v\n%s", err, stderr)
	}
	if len(sent) != 0 {
		t.Fatalf("a file that does not compile sent %v", sent)
	}
}
