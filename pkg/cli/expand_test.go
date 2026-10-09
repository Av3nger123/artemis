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
	out, stderr, err := runCLI(t, "expand", "testdata/collections/checkout.art")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	checkFileGolden(t, "testdata/collections/checkout.expanded.art", out)

	// The golden's shape, spelled out, so -update cannot quietly bless a
	// wrong one.
	order := []string{
		`var url = env("API_URL")`,
		`secret var login_password = "s3cret"`,
		"# from auth.login (testdata/collections/auth.art:2) via testdata/collections/checkout.art:6",
		`step "auth.login"`,
		`secret var bad_password = "wrong"`,
		"# from auth.login (testdata/collections/auth.art:2) via testdata/collections/checkout.art:8",
		`step "bad"`,
		"capture bad_token",
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
	if strings.Contains(out, "use ") || strings.Contains(out, "import ") {
		t.Errorf("the expanded scenario still has a use or an import:\n%s", out)
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
	if _, _, err := runCLI(t, "expand", "testdata/collections/checkout.art", "-o", out); err == nil {
		t.Fatal("want an error without --force")
	}
	if b, _ := os.ReadFile(out); string(b) != "keep" { //nolint:gosec // a path this test wrote
		t.Fatalf("the file was overwritten without --force: %q", b)
	}
	if _, stderr, err := runCLI(t, "expand", "testdata/collections/checkout.art", "-o", out, "--force"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	b, _ := os.ReadFile(out) //nolint:gosec // a path this test wrote
	if !strings.Contains(string(b), `step "auth.login"`) {
		t.Fatalf("--force did not write the expanded scenario: %q", b)
	}
}

func TestExpandReportsABrokenCollection(t *testing.T) {
	out, stderr, err := runCLI(t, "expand", "testdata/collections/broken.art")
	if err == nil {
		t.Fatal("want a non-zero exit")
	}
	if out != "" || !strings.Contains(stderr, "broken_coll.art:4") {
		t.Fatalf("stdout %q\nstderr %s", out, stderr)
	}
}

// A step a flow's own use brought in names the flow's request as where it is
// from, and the scenario's use line as how it got here: the line a reader of
// the scenario can find, not one inside the collection.
func TestExpandNamesTheScenariosUseForANestedStep(t *testing.T) {
	out, stderr, err := runCLI(t, "expand", "testdata/collections/nested.art")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	checkFileGolden(t, "testdata/collections/nested.expanded.art", out)
	for _, want := range []string{
		"# from auth.login (testdata/collections/auth.art:2) via testdata/collections/nested.art:6\n  step \"session.start / auth.login\"",
		"# from session.start (testdata/collections/session.art:4) via testdata/collections/nested.art:6\n  step \"session.start / me\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "via testdata/collections/session.art") {
		t.Errorf("an origin names a use inside the collection:\n%s", out)
	}
}
