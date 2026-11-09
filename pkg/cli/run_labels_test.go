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
