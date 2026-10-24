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

// readCaptures reads what --captures wrote.
func readCaptures(t *testing.T, path string) itemCaptures {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the captures file: %v", err)
	}
	var got itemCaptures
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the captures file is not JSON: %v\n%s", err, b)
	}
	return got
}

func TestRunOneRequestOfACollection(t *testing.T) {
	srv := authServer(t)
	dir := t.TempDir()
	captures := filepath.Join(dir, "captures.json")
	traces := filepath.Join(dir, "traces")
	out, stderr, err := runCLI(t, "run", "testdata/collections/auth.art", "--item", "auth.login",
		"--arg", "user=alice", "--arg", "password=s3cret", "--arg", "base="+srv.URL,
		"--report", "json", "--captures", captures, "--trace", traces)
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	if !strings.Contains(out, `"auth.login"`) {
		t.Errorf("the report has no auth.login step:\n%s", out)
	}
	if strings.Contains(out, "s3cret") || strings.Contains(stderr, "s3cret") {
		t.Error("a secret parameter leaked into the report")
	}
	// The response a client shows comes from the step's trace.
	shot, err := os.ReadFile(filepath.Join(traces, "auth-login-auth-login.json"))
	if err != nil {
		entries, _ := os.ReadDir(traces)
		t.Fatalf("no trace for the request: %v (the folder holds %v)", err, entries)
	}
	if !strings.Contains(string(shot), `"status"`) || strings.Contains(string(shot), "s3cret") {
		t.Errorf("the trace has no status, or holds the secret:\n%s", shot)
	}
	got := readCaptures(t, captures)
	if got.Values["token"] != "t" {
		t.Errorf("token = %v, want t", got.Values["token"])
	}
	if len(got.Secret) != 1 || got.Secret[0] != "token" {
		t.Errorf("secret = %v, want [token]", got.Secret)
	}
	info, err := os.Stat(captures)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the captures file is %v, want -rw-------", info.Mode().Perm())
	}
}

func TestItemArgumentsFromAFile(t *testing.T) {
	srv := authServer(t)
	dir := t.TempDir()
	args := filepath.Join(dir, "args.json")
	if err := os.WriteFile(args, []byte(`{"user": "alice", "password": "s3cret", "base": "`+srv.URL+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runCLI(t, "run", "testdata/collections/auth.art", "--item", "auth.login", "--args", args, "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
}

// A --arg is a value, never source: what looks like an interpolation is sent
// as the characters it is.
func TestAnArgumentIsSentAsWritten(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body.Username
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"access_token":"t"}}`))
	}))
	t.Cleanup(srv.Close)
	_, stderr, err := runCLI(t, "run", "testdata/collections/auth.art", "--item", "auth.login",
		"--arg", `user=a"b ${base} \n`, "--arg", "password=x", "--arg", "base="+srv.URL, "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, stderr)
	}
	if got != `a"b ${base} \n` {
		t.Fatalf("the service got user %q", got)
	}
}

func TestRunEveryItemOfACollection(t *testing.T) {
	// Every request of the collection, in the order written.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"access_token":"t"}}`))
	}))
	t.Cleanup(srv.Close)
	out, stderr, err := runCLI(t, "run", "testdata/collections/session.art", "--item", "session", "--arg", "base="+srv.URL, "--report", "json")
	if err != nil {
		t.Fatalf("run failed: %v\n%s\n%s", err, out, stderr)
	}
	for _, want := range []string{"session.login", "session.me"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no %s step:\n%s", want, out)
		}
	}
}

func TestAnItemMissingAnArgumentDoesNotRun(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	t.Cleanup(srv.Close)
	_, stderr, err := runCLI(t, "run", "testdata/collections/auth.art", "--item", "auth.login", "--arg", "user=alice", "--arg", "base="+srv.URL)
	if err == nil {
		t.Fatal("want a non-zero exit")
	}
	if !strings.Contains(stderr, "password") {
		t.Errorf("the error does not name the missing parameter:\n%s", stderr)
	}
	if hit {
		t.Error("a request went out")
	}
}

func TestAnItemThatIsNotThere(t *testing.T) {
	_, _, err := runCLI(t, "run", "testdata/collections/auth.art", "--item", "auth.logout")
	if err == nil || !strings.Contains(err.Error(), "auth has no item logout") || !strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = runCLI(t, "run", "testdata/collections/auth.art", "--item", "billing")
	if err == nil || !strings.Contains(err.Error(), "no collection billing") {
		t.Fatalf("err = %v", err)
	}
}

func TestItemFlagsNeedAnItemAndAFile(t *testing.T) {
	_, _, err := runCLI(t, "run", "testdata/collections/auth.art", "--arg", "user=alice")
	if err == nil || !strings.Contains(err.Error(), "--item") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = runCLI(t, "run", "testdata/collections", "--item", "auth.login")
	if err == nil || !strings.Contains(err.Error(), "a collection file") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = runCLI(t, "run", "testdata/collections/auth.art", "--item", "auth.login", "--arg", "no-equals")
	if err == nil || !strings.Contains(err.Error(), "name=value") {
		t.Fatalf("err = %v", err)
	}
}

// A draft written to a scratch folder reads its imports from where the real
// file is.
func TestAstAndParseReadAFileAsIfItWereElsewhere(t *testing.T) {
	draft := filepath.Join(t.TempDir(), "draft.art")
	src := "import \"auth.art\"\n\nscenario \"s\" {\n  use auth.login { user = \"a\", password = \"b\", base = \"http://x\" }\n}\n"
	if err := os.WriteFile(draft, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	as := filepath.Join("testdata", "collections", "draft.art")
	if _, stderr, err := runCLI(t, "ast", "-f", draft); err == nil {
		t.Fatalf("without --as the import should not resolve\n%s", stderr)
	}
	out, stderr, err := runCLI(t, "ast", "-f", draft, "--as", as)
	if err != nil || !strings.Contains(out, `"use"`) {
		t.Fatalf("ast --as: %v\n%s\n%s", err, out, stderr)
	}
	out, stderr, err = runCLI(t, "parse", "-f", draft, "--as", as, "--json")
	if err != nil || !strings.Contains(out, `"diagnostics"`) {
		t.Fatalf("parse --as: %v\n%s\n%s", err, out, stderr)
	}
}
