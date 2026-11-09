package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --item reports its scenario and step with ids, and names the step's trace
// after them.
func TestStandaloneItemIDs(t *testing.T) {
	srv := authServer(t)
	dir := writeTree(t, t.TempDir(), map[string]string{"collections/auth.art": newAuth})
	traces := filepath.Join(dir, "traces")
	out, stderr, err := runCLI(t, "run", filepath.Join(dir, "collections", "auth.art"), "--item", "auth.login",
		"--arg", "user=a", "--arg", "base="+srv.URL, "--report", "json", "--trace", traces)
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	for _, want := range []string{`"id": "auth_login"`, `"id": "login"`, `"name": "auth.login"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no %s:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(traces, "auth_login-login.json")); err != nil {
		entries, _ := os.ReadDir(traces)
		t.Errorf("no trace auth_login-login.json; the folder holds %v", entries)
	}
}

// A labelled item's step reads its label; the scenario is still auth.login.
func TestStandaloneItemTakesTheItemLabel(t *testing.T) {
	srv := authServer(t)
	src := strings.Replace(newAuth, "request login {", "request login as \"Log in\" {", 1)
	dir := writeTree(t, t.TempDir(), map[string]string{"collections/auth.art": src})
	out, stderr, err := runCLI(t, "run", filepath.Join(dir, "collections", "auth.art"), "--item", "auth.login",
		"--arg", "user=a", "--arg", "base="+srv.URL, "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	if !strings.Contains(out, `"label": "Log in"`) {
		t.Errorf("the step does not carry the item's label:\n%s", out)
	}
}

// artemis list --json names each scenario by its label, and carries the id
// and the label apart, with the flow's label.
func TestListJSONCarriesIDsAndLabels(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"flows/checkout.art":     "flow checkout as \"Checkout\" {\n  step a {\n    get \"http://x\"\n  }\n}\n",
		"scenarios/checkout.art": "flow \"../flows/checkout.art\"\n\nscenario with_coupon as \"With a coupon\" {}\n",
	})
	out, stderr, err := runCLI(t, "list", filepath.Join(dir, "scenarios"), "--json")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, stderr)
	}
	for _, want := range []string{`"id":"with_coupon"`, `"label":"With a coupon"`, `"name":"With a coupon"`, `"flow_label":"Checkout"`} {
		if !strings.Contains(strings.ReplaceAll(out, " ", ""), strings.ReplaceAll(want, " ", "")) {
			t.Errorf("list --json has no %s:\n%s", want, out)
		}
	}
}
