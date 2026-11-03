package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// oldProject is a folder in the old forms: a suite, a collection with params,
// and a scenario that uses it.
var oldProject = map[string]string{
	"suite.art": "suite {\n  var url = \"http://x\"\n}\n",
	"collections/auth.art": "collection \"auth\" {\n  request login(user) {\n    post \"/l\" {\n      body = {\"user\": user}\n    }\n" +
		"    capture token = body.token\n  }\n}\n",
	"checkout.art": "import \"collections/auth.art\"\n\nscenario \"checkout\" {\n  use auth.login { user = \"a\" }\n\n" +
		"  step \"pay\" {\n    post \"${suite.url}/pay\"\n    expect status == 200\n  }\n}\n",
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for k, v := range files {
		p := filepath.Join(dir, filepath.FromSlash(k))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// listFiles is every file under dir, by slash path relative to it.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func unchanged(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for k, v := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(k)))
		if err != nil || string(b) != v {
			t.Errorf("%s changed: %v\n%s", k, err, b)
		}
	}
}

// A folder of old files becomes the new files under -o, at the same paths:
// the collection where it was, a flow and a scenario file for the scenario,
// and no suite.art. The suite's note is on stderr, and the originals are as
// they were.
func TestMigrateCommandWritesNewFiles(t *testing.T) {
	dir := writeProject(t, oldProject)
	out := filepath.Join(t.TempDir(), "new")

	stdout, stderr, err := runCLI(t, "migrate", "-f", dir, "-o", out)
	if err != nil {
		t.Fatalf("migrate = %v\nstderr:\n%s", err, stderr)
	}
	want := []string{"collections/auth.art", "flows/checkout.art", "scenarios/checkout.art"}
	if got := listFiles(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("wrote %q, want %q", got, want)
	}
	for _, w := range want {
		if !strings.Contains(stdout, filepath.Join(out, filepath.FromSlash(w))) {
			t.Errorf("stdout does not name %s:\n%s", w, stdout)
		}
	}
	if !strings.Contains(stderr, "note: suite.art: its setup now runs once per scenario, not once per run") {
		t.Errorf("stderr has no note for the suite:\n%s", stderr)
	}
	unchanged(t, dir, oldProject)
}

// One file brings what it needs: the suite above it is copied into its flow,
// and the collection it imports is migrated beside it.
func TestMigrateOneFileBringsItsImports(t *testing.T) {
	dir := writeProject(t, oldProject)
	out := t.TempDir()

	if _, stderr, err := runCLI(t, "migrate", "-f", filepath.Join(dir, "checkout.art"), "-o", out); err != nil {
		t.Fatalf("migrate = %v\nstderr:\n%s", err, stderr)
	}
	want := []string{"collections/auth.art", "flows/checkout.art", "scenarios/checkout.art"}
	if got := listFiles(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

// -o naming the input folder would replace the collection with its new form,
// and migrate never changes an original: it refuses, and writes nothing.
func TestMigrateNeverWritesOverTheOriginals(t *testing.T) {
	dir := writeProject(t, oldProject)
	if _, _, err := runCLI(t, "migrate", "-f", dir, "-o", dir); err == nil || !strings.Contains(err.Error(), "would write over") {
		t.Fatalf("migrate -o <the input> = %v, want a refusal", err)
	}
	unchanged(t, dir, oldProject)
	if got := listFiles(t, dir); len(got) != len(oldProject) {
		t.Errorf("the folder holds %q after a refusal", got)
	}
}

// What a migrated file says, once the printer writes flows and scenario files.
func TestMigrateCommandWritesTheNewForms(t *testing.T) {
	t.Skip("integration: needs Tasks 2-6,9")
	dir := writeProject(t, oldProject)
	out := t.TempDir()
	if _, stderr, err := runCLI(t, "migrate", "-f", dir, "-o", out); err != nil {
		t.Fatalf("migrate = %v\nstderr:\n%s", err, stderr)
	}
	for path, want := range map[string]string{
		"scenarios/checkout.art": "flow \"../flows/checkout.art\"\n\nscenario \"checkout\" {}\n",
		"flows/checkout.art": "import \"../collections/auth.art\"\n\nflow checkout {\n  var url = \"http://x\"\n\n" +
			"  step \"auth.login\" = auth.login { user = \"a\" }\n\n" +
			"  step \"pay\" {\n    post \"${url}/pay\"\n    expect status == 200\n  }\n}\n",
		"collections/auth.art": "collection \"auth\" {\n  request login {\n    var user\n\n" +
			"    post \"/l\" { body = {\"user\": user} }\n    var token = body.token\n  }\n}\n",
	} {
		b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(path)))
		if err != nil || string(b) != want {
			t.Errorf("%s = %v\n%s\nwant\n%s", path, err, b, want)
		}
	}
	if _, stderr, err := runCLI(t, "parse", "-f", filepath.Join(out, "scenarios", "checkout.art")); err != nil {
		t.Errorf("the migrated scenario file does not compile: %v\n%s", err, stderr)
	}
}
