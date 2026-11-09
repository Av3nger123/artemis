package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
				// Still a token-shaped body, so failing.art -- which keeps
				// the request's capture -- fails on its status expect alone.
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
	out, stderr, err := runCLI(t, "run", "testdata/collections/scenarios/checkout.art", "--report", "json")
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

func TestParseAScenarioThatUsesACollection(t *testing.T) {
	out, stderr, err := runCLI(t, "parse", "-f", "testdata/collections/scenarios/checkout.art")
	if err != nil {
		t.Fatalf("parse failed: %v\n%s", err, stderr)
	}
	if out != "testdata/collections/scenarios/checkout.art: ok\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestBrokenCollectionReportedOnceAgainstTheCollection(t *testing.T) {
	_, stderr, err := runCLI(t, "parse", "-f", "testdata/collections/scenarios/broken.art")
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
	out, stderr, err := runCLI(t, "fmt", "testdata/collections/flows/checkout.art")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(out, `step auth_login as "auth.login" = auth.login`) || strings.Contains(out, `/token"`) {
		t.Fatalf("fmt must print the file as written:\n%s", out)
	}
}

// A failure in a step a use brought in names the collection file its line was
// written in -- on the console, in the JSON report's failures, and on the
// step and assertion -- not the scenario file with a line from another file.
func TestAFailureInACollectionNamesTheCollectionFile(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	stdout, stderr, err := runCLI(t, "run", "testdata/collections/scenarios/failing.art", "--report", "json")
	if err == nil {
		t.Fatal("want the run to fail")
	}
	if !strings.Contains(stderr, "testdata/collections/auth.art:8") {
		t.Errorf("the console does not name auth.art:8:\n%s", stderr)
	}
	if strings.Contains(stderr, "failing.art:8") {
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
	if len(doc.Failures) != 1 || doc.Failures[0].File != "testdata/collections/auth.art" || doc.Failures[0].Line != 8 {
		t.Errorf("failures = %+v", doc.Failures)
	}
	step := doc.Scenarios[0].Steps[0]
	if step.File != "testdata/collections/auth.art" || step.Line != 2 {
		t.Errorf("step = %+v", step)
	}
	if a := step.Assertions[0]; a.File != "testdata/collections/auth.art" || a.Line != 8 {
		t.Errorf("assertion = %+v", a)
	}
	if !strings.Contains(stdout, `"file": "testdata/collections/auth.art"`) &&
		!strings.Contains(stdout, `"file":"testdata/collections/auth.art"`) {
		t.Errorf("no file key on the step:\n%s", stdout)
	}
}

// Every step of a scenario file's scenario carries the file it was written
// in, because none was written in the scenario file: a step the flow writes
// in place names the flow file, and a step that calls an item names the
// collection's.
func TestAStepNamesTheFileItWasWrittenIn(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	stdout, _, err := runCLI(t, "run", "testdata/collections/scenarios/checkout.art", "--report", "json")
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
		want := "testdata/collections/auth.art"
		if st["name"] == "orders" {
			want = "testdata/collections/flows/checkout.art"
		}
		if st["file"] != want {
			t.Errorf("step %v: file = %v, want %s", st["name"], st["file"], want)
		}
	}
}

// A file of only collections runs nothing and passes, so a folder run is not
// upset by a collections/ directory beside the scenarios.
func TestRunAFolderWithACollectionOnlyFile(t *testing.T) {
	srv := authServer(t)
	t.Setenv("API_URL", srv.URL)
	dir := writeTree(t, t.TempDir(), map[string]string{
		"auth.art":               mustRead(t, "testdata/collections/auth.art"),
		"flows/checkout.art":     mustRead(t, "testdata/collections/flows/checkout.art"),
		"scenarios/checkout.art": mustRead(t, "testdata/collections/scenarios/checkout.art"),
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

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // a testdata path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
