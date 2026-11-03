package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The tag expression's grammar and precedence: not binds tightest, then and,
// then or; parentheses group.
func TestTagExpressions(t *testing.T) {
	cases := []struct {
		expr string
		tags []string
		want bool
	}{
		{"smoke", []string{"smoke"}, true},
		{"smoke", []string{"slow"}, false},
		{"smoke and not slow", []string{"smoke"}, true},
		{"smoke and not slow", []string{"smoke", "slow"}, false},
		{"api or ui and smoke", []string{"api"}, true},
		{"(api or ui) and smoke", []string{"api"}, false},
		{"(api or ui) and smoke", []string{"ui", "smoke"}, true},
		{"not not smoke", []string{"smoke"}, true},
	}
	for _, c := range cases {
		sel, err := newSelector(c.expr, "")
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		if got := sel.selects("x", c.tags); got != c.want {
			t.Errorf("%q over %v = %v, want %v", c.expr, c.tags, got, c.want)
		}
	}
	for _, bad := range []string{"smoke and", "(smoke", "and smoke", "smoke )", "smoke not"} {
		if _, err := newSelector(bad, ""); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, err := newSelector("", "("); err == nil {
		t.Error("an uncompilable --grep was accepted")
	}
}

// taggedTree is a folder of two scenario files: one flow run by two tagged
// scenarios, each reading its address from its own environment variable, and
// one whose flow reads a variable that is never set.
func taggedTree(t *testing.T, url string) string {
	t.Helper()
	return writeTree(t, t.TempDir(), map[string]string{
		"suite/a.art": fmt.Sprintf(`flow checkout {
  var u
  step "s" {
    get "${u}"
    expect status == 200
  }
}

scenario "checkout smoke" {
  tags = ["smoke", "api"]
  u = env("SMOKE_URL", %[1]q)
}

scenario "checkout slow" {
  tags = ["slow", "api"]
  u = env("SLOW_URL", %[1]q)
}
`, url),
		"suite/b.art": `flow needs_a_secret {
  var key = env("NIGHTLY_KEY_NEVER_SET")
  step "s" { run "true" }
}

scenario "needs a secret" { tags = ["nightly", "api"] }
`,
	})
}

// --tags runs what it selects and records the rest as skipped, with why; a
// variable only an unselected scenario reads is not checked.
func TestTagsSelectWhatRuns(t *testing.T) {
	srv, _ := recordingServer(t, 200)
	dir := taggedTree(t, srv.URL)

	stdout, stderr, err := runCLI(t, "run", dir+"/suite", "--tags", "api and smoke", "--report", "json")
	if err != nil {
		t.Fatalf("run: %v\n%s\n%s", err, stdout, stderr)
	}
	var doc struct {
		Scenarios []struct {
			Name   string   `json:"name"`
			Status string   `json:"status"`
			Error  string   `json:"error"`
			Tags   []string `json:"tags"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("report: %v\n%s", err, stdout)
	}
	got := map[string]string{}
	for _, sc := range doc.Scenarios {
		got[sc.Name] = sc.Status
		if sc.Name == "checkout smoke" && strings.Join(sc.Tags, ",") != "smoke,api" {
			t.Errorf("tags = %v, want smoke and api", sc.Tags)
		}
		if sc.Status == "skip" && !strings.Contains(sc.Error, `not selected by --tags "api and smoke"`) {
			t.Errorf("%s was skipped saying %q", sc.Name, sc.Error)
		}
	}
	if got["checkout smoke"] != "pass" || got["checkout slow"] != "skip" || got["needs a secret"] != "skip" {
		t.Errorf("statuses = %v", got)
	}
}

// --grep selects by name, and composes with --tags.
func TestGrepSelectsByName(t *testing.T) {
	srv, _ := recordingServer(t, 200)
	dir := taggedTree(t, srv.URL)
	stdout, _, err := runCLI(t, "run", dir+"/suite", "--grep", "^checkout", "--tags", "not nightly", "--report", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Count(stdout, `"status": "pass"`) < 2 {
		t.Errorf("both checkouts should have passed:\n%s", stdout)
	}
}

// A selection of nothing is an error, not a run of nothing that passes; and a
// tag no scenario has is warned about, with the nearest one.
func TestNothingSelectedIsAnError(t *testing.T) {
	srv, _ := recordingServer(t, 200)
	dir := taggedTree(t, srv.URL)
	_, stderr, err := runCLI(t, "run", dir+"/suite", "--tags", "smok")
	if err == nil || !strings.Contains(err.Error(), `no scenario is selected by --tags "smok"`) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(stderr, `no scenario in this run is tagged "smok"; did you mean "smoke"?`) {
		t.Errorf("no warning about the typo:\n%s", stderr)
	}
}

// list prints what a run would run -- under each file, with its tags -- and
// selects as run does, running nothing.
func TestListShowsWhatWouldRun(t *testing.T) {
	srv, rec := recordingServer(t, 200)
	dir := taggedTree(t, srv.URL)

	stdout, _, err := runCLI(t, "list", dir+"/suite", "--tags", "api and not nightly")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"checkout smoke  [smoke, api]", "checkout slow   [slow, api]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "needs a secret") || strings.Contains(stdout, "flow.art") {
		t.Errorf("listed what was not selected, or a flow file:\n%s", stdout)
	}
	if len(rec.seen()) != 0 {
		t.Error("list sent a request")
	}

	stdout, _, err = runCLI(t, "list", dir+"/suite", "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var doc struct {
		Scenarios []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || len(doc.Scenarios) != 3 {
		t.Errorf("list --json = %v, %s", err, stdout)
	}
}
