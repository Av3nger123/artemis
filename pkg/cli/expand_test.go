package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkFileGolden holds got to path, or rewrites path under -update.
func checkFileGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a testdata path
	if err != nil {
		t.Fatalf("reading %s: %v (run with -update to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func TestExpandPrintsTheIntermediaryScenario(t *testing.T) {
	out, stderr, err := runCLI(t, "expand", "testdata/collections/scenarios/checkout.art")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	checkFileGolden(t, "testdata/collections/checkout.expanded.art", out)

	// The golden's shape, spelled out, so -update cannot quietly bless a
	// wrong one.
	order := []string{
		`var url = env("API_URL")`,
		"# from auth.login (testdata/collections/auth.art:2) via testdata/collections/flows/checkout.art:6",
		`step "auth.login"`,
		`"password": "s3cret"`,
		"# from auth.login (testdata/collections/auth.art:2) via testdata/collections/flows/checkout.art:8",
		`step "bad"`,
		`"password": "wrong"`,
		"expect status == 401",
		`step "orders"`,
	}
	rest := out
	for _, want := range order {
		i := strings.Index(rest, want)
		if i < 0 {
			t.Fatalf("missing, or out of order: %q\n%s", want, out)
		}
		rest = rest[i+len(want):]
	}
	bad := out[strings.Index(out, `step "bad"`):strings.Index(out, `step "orders"`)]
	if strings.Contains(bad, "== 200") {
		t.Errorf("drop expects left the request's expect in step bad:\n%s", bad)
	}
	if strings.Contains(bad, "var token") {
		t.Errorf("drop vars left the request's var in step bad:\n%s", bad)
	}
	if strings.Contains(out, " = auth.") || strings.Contains(out, "import ") || strings.Contains(out, "flow ") {
		t.Errorf("the expanded scenario still has an item call, an import or a flow:\n%s", out)
	}
	// What expand prints is a scenario artemis accepts on its own.
	if u := frontEnd("expanded.art", out); u.Bag.HasErrors() {
		t.Errorf("the expanded scenario does not compile: %v", u.Bag.All())
	}
}

func TestExpandRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.art")
	if err := os.WriteFile(out, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLI(t, "expand", "testdata/collections/scenarios/checkout.art", "-o", out); err == nil {
		t.Fatal("want an error without --force")
	}
	if b, _ := os.ReadFile(out); string(b) != "keep" { //nolint:gosec // a path this test wrote
		t.Fatalf("the file was overwritten without --force: %q", b)
	}
	if _, stderr, err := runCLI(t, "expand", "testdata/collections/scenarios/checkout.art", "-o", out, "--force"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	b, _ := os.ReadFile(out) //nolint:gosec // a path this test wrote
	if !strings.Contains(string(b), `step "auth.login"`) {
		t.Fatalf("--force did not write the expanded scenario: %q", b)
	}
}

func TestExpandReportsABrokenCollection(t *testing.T) {
	out, stderr, err := runCLI(t, "expand", "testdata/collections/scenarios/broken.art")
	if err == nil {
		t.Fatal("want a non-zero exit")
	}
	if out != "" || !strings.Contains(stderr, "broken_coll.art:4") {
		t.Fatalf("stdout %q\nstderr %s", out, stderr)
	}
}
