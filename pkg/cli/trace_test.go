package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/trace"
)

// ART-55 end to end: a real run with --trace, and the one thing that must be
// true of the files it leaves behind.
//
// The scenario is the README's own shape -- a token request whose password comes
// from a secret var, and whose token is a secret capture used by the next step --
// because that is the shape where every withholding rule fires at once.
func TestRunWritesATraceAndWithholdsTheCredential(t *testing.T) {
	initLog(t)
	t.Setenv("ART_TRACE_PW", "hunter2")

	srv := okServer(t, 200, `{"data":{"access_token":"t0ken-abc","expires_in":3600}}`)
	root := writeTree(t, t.TempDir(), map[string]string{
		"checkout.art": `scenario "checkout" {
  secret var pw = env("ART_TRACE_PW")

  step "get a token" {
    post "` + srv.URL + `" {
      header "Content-Type" = "application/json"
      body = {"username": "alice", "password": pw}
    }
    expect status == 200
    secret capture token = body.data.access_token
  }

  step "list the orders" {
    get "` + srv.URL + `" {
      header "Authorization" = "Bearer ${token}"
    }
    expect status == 200
  }
}`,
	})

	dir := filepath.Join(t.TempDir(), "traces")
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFilesWith(&runtimeEnv{
		reg:    executor.Default(),
		traces: trace.New(dir),
	}, files, report.Discard(), io.Discard)

	if !run.Passed() {
		t.Fatalf("run did not pass: %s", runFailedError(run))
	}

	// Every step gets a trace, pass or fail.
	steps := run.Scenarios[0].Steps
	if len(steps) != 2 {
		t.Fatalf("ran %d steps, want 2", len(steps))
	}
	for _, st := range steps {
		if st.Trace == "" {
			t.Errorf("step %q has no trace path", st.Name)
		}
	}

	// Nothing anywhere in the folder may hold either credential.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 2 {
		t.Errorf("wrote %d files, want one per step", len(entries))
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"hunter2", "t0ken-abc"} {
			if bytes.Contains(raw, []byte(leak)) {
				t.Errorf("%s holds %q:\n%s", e.Name(), leak, raw)
			}
		}
	}

	// The first step: the password is withheld in what it sent, the token is
	// withheld in what it saw, and everything else survives.
	first := readTrace(t, steps[0].Trace)
	body := first.Sent["body"].(map[string]any)
	if body["password"] != trace.Redacted {
		t.Errorf("sent body password = %v, want withheld", body["password"])
	}
	if body["username"] != "alice" {
		t.Errorf("sent body username = %v, want it kept", body["username"])
	}
	data := first.Observed["body"].(map[string]any)["data"].(map[string]any)
	if data["access_token"] != trace.Redacted {
		t.Errorf("observed access_token = %v, want withheld", data["access_token"])
	}
	if data["expires_in"] != float64(3600) {
		t.Errorf("observed expires_in = %v, want it kept", data["expires_in"])
	}
	if first.Observed["status"] != float64(200) {
		t.Errorf("observed status = %v, want it kept", first.Observed["status"])
	}

	// The second step: the Authorization header interpolates the secret capture,
	// so it is withheld, and the other header is not.
	second := readTrace(t, steps[1].Trace)
	headers := second.Sent["headers"].(map[string]any)
	if headers["Authorization"] != trace.Redacted {
		t.Errorf("Authorization = %v, want withheld", headers["Authorization"])
	}
}

// With no --trace, nothing is written and no step carries a path.
func TestRunWritesNoTraceUnlessAsked(t *testing.T) {
	initLog(t)
	srv := okServer(t, 200, `{"status":"ok"}`)
	root := writeTree(t, t.TempDir(), map[string]string{
		"a.art": namedScenarioArt("first", srv.URL, "ok"),
	})
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFiles(executor.Default(), files, report.Discard(), io.Discard)
	if !run.Passed() {
		t.Fatalf("run did not pass: %s", runFailedError(run))
	}
	if got := run.Scenarios[0].Steps[0].Trace; got != "" {
		t.Errorf("Trace = %q, want empty with no --trace", got)
	}
}

// A step that could not run at all still leaves a trace: there is no response to
// read, so what it sent and why it failed are the whole of the evidence.
func TestRunTracesAStepThatCouldNotRun(t *testing.T) {
	initLog(t)
	root := writeTree(t, t.TempDir(), map[string]string{
		// A port nothing is listening on.
		"a.art": namedScenarioArt("first", "http://127.0.0.1:1", "ok"),
	})
	dir := filepath.Join(t.TempDir(), "traces")
	files, err := discover(root)
	if err != nil {
		t.Fatalf("discover() = %v, want nil", err)
	}
	run := runFilesWith(&runtimeEnv{
		reg:    executor.Default(),
		traces: trace.New(dir),
	}, files, report.Discard(), io.Discard)

	if run.Passed() {
		t.Fatal("the run passed, so this test proves nothing")
	}
	st := run.Scenarios[0].Steps[0]
	if st.Trace == "" {
		t.Fatal("a step that could not run left no trace")
	}
	rec := readTrace(t, st.Trace)
	if rec.Error == "" {
		t.Error("the trace does not say why the step could not run")
	}
	if rec.Observed != nil {
		t.Errorf("Observed = %v, want none: there was no response", rec.Observed)
	}
	if rec.Sent["url"] == nil {
		t.Error("the trace does not say what was sent, which is all the evidence there is")
	}
}

func readTrace(t *testing.T, path string) trace.Record {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the trace %s: %v", path, err)
	}
	var rec trace.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decoding the trace %s: %v\n%s", path, err, raw)
	}
	if strings.TrimSpace(string(raw)) == "" {
		t.Fatalf("the trace %s is empty", path)
	}
	return rec
}
