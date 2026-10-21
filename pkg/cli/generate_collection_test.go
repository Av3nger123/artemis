package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default mode writes a collection file beside -o and a scenario that
// imports it, and the two compile together with no diagnostics.
func TestGenerateWritesACollectionAndAScenario(t *testing.T) {
	path := writeCollection(t, "ping.postman_collection.json", smallCollection)
	out := filepath.Join(filepath.Dir(path), "ping.art")

	stdout, stderr, err := executeArgs(t, "generate", "-f", path, "-o", out)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want nothing", stdout, stderr)
	}

	coll := readFile(t, filepath.Join(filepath.Dir(path), "ping.collection.art"))
	if !strings.Contains(coll, `collection "ping" {`) || !strings.Contains(coll, "request health_ping() {") {
		t.Errorf("the collection file holds\n%s", coll)
	}
	scen := readFile(t, out)
	if !strings.Contains(scen, `import "ping.collection.art"`) || !strings.Contains(scen, "use ping.health_ping") {
		t.Errorf("the scenario file holds\n%s", scen)
	}

	bag := frontEnd(out, scen).Bag
	for _, d := range bag.All() {
		t.Errorf("%s: %s: %s", d.Span.File, d.Code, d.Message)
	}
}

// Two files cannot go to stdout, so the default mode needs -o, and the error
// names the way to get one file.
func TestGenerateCollectionNeedsOut(t *testing.T) {
	path := writeCollection(t, "ping.postman_collection.json", smallCollection)

	stdout, _, err := executeArgs(t, "generate", "-f", path)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error\nstdout:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "-o") || !strings.Contains(err.Error(), "--flat") {
		t.Errorf("error = %v, want -o and --flat named", err)
	}
}

// The clobber guard covers the collection file too: an existing one is refused
// without --force, and then neither file is written.
func TestGenerateRefusesAnExistingCollectionFile(t *testing.T) {
	path := writeCollection(t, "ping.postman_collection.json", smallCollection)
	dir := filepath.Dir(path)
	out := filepath.Join(dir, "ping.art")
	coll := filepath.Join(dir, "ping.collection.art")
	const mine = "# my own collection\n"
	if err := os.WriteFile(coll, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeArgs(t, "generate", "-f", path, "-o", out)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "ping.collection.art") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want the collection file and --force named", err)
	}
	if got := readFile(t, coll); got != mine {
		t.Errorf("the collection file changed:\n%s", got)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("the scenario file was written anyway")
	}

	if _, stderr, err := executeArgs(t, "generate", "-f", path, "-o", out, "--force"); err != nil {
		t.Fatalf("with --force: Execute() = %v\nstderr:\n%s", err, stderr)
	}
	if got := readFile(t, coll); !strings.Contains(got, "request health_ping()") {
		t.Errorf("--force did not replace the collection file:\n%s", got)
	}
}

// A Postman file with no requests has nothing to put in a collection: the
// command says so and writes no file.
func TestGenerateRefusesACollectionWithNoRequests(t *testing.T) {
	path := writeCollection(t, "empty.postman_collection.json",
		`{"info":{"name":"empty"},"item":[{"name":"a folder","item":[]}]}`)
	dir := filepath.Dir(path)
	out := filepath.Join(dir, "empty.art")

	_, _, err := executeArgs(t, "generate", "-f", path, "-o", out)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "no requests") {
		t.Errorf("error = %v, want it to say the collection holds no requests", err)
	}
	for _, f := range []string{out, filepath.Join(dir, "empty.collection.art")} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s was written", f)
		}
	}
}

// The fixture runs to the same counts in both modes: the collection output
// sends the same requests and checks the same expects as the flat output.
// The server fails one request on purpose, so the fail counts are compared too.
func TestGeneratedCollectionRunsLikeTheFlatOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/orders":
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/orders/search":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ACCESS_TOKEN", "t")
	t.Setenv("OPERATOR", "ada")

	src := strings.ReplaceAll(readFile(t, goldenCollection), "https://api.example.com", srv.URL)
	path := writeCollection(t, "kitchen_sink.postman_collection.json", src)
	dir := filepath.Dir(path)
	flat := filepath.Join(dir, "flat", "kitchen_sink.art")
	coll := filepath.Join(dir, "coll", "kitchen_sink.art")

	if _, stderr, err := executeArgs(t, "generate", "-f", path, "--flat", "-o", flat); err != nil {
		t.Fatalf("generate --flat: %v\n%s", err, stderr)
	}
	if _, stderr, err := executeArgs(t, "generate", "-f", path, "-o", coll); err != nil {
		t.Fatalf("generate: %v\n%s", err, stderr)
	}

	want := runCounts(t, flat)
	got := runCounts(t, coll)
	if got != want {
		t.Errorf("collection run counts = %+v, flat run counts = %+v", got, want)
	}
	if want.Steps.Failed != 1 || want.Steps.Total != 6 {
		t.Errorf("flat run counts = %+v, want 6 steps and 1 failed", want)
	}
}

// runCounts runs path and returns the counts of its JSON report.
func runCounts(t *testing.T, path string) jsonCountsForTest {
	t.Helper()
	stdout, stderr, _ := runCLI(t, "run", path, "--report", "json")
	var doc struct {
		Counts jsonCountsForTest `json:"counts"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("run %s: the report is not JSON: %v\nstdout:\n%s\nstderr:\n%s", path, err, stdout, stderr)
	}
	return doc.Counts
}

type tallyForTest struct{ Total, Passed, Failed, Errored, Skipped int }

type jsonCountsForTest struct {
	Scenarios, Steps, Assertions tallyForTest
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
