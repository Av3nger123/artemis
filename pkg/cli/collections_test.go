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
