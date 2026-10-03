package httpstep

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The roots this file binds must be exactly the ones pkg/dsl/check lets a .art
// api step name. A root the checker admits and nothing binds is an `expect`
// that errors at run time with no mistake in the file; a root bound here and
// unknown to the checker is one nobody can write.
func TestObservedRootsMatchTheChecker(t *testing.T) {
	srv := httptest.NewServer(jsonAt(http.StatusOK, `{"ok": true}`))
	defer srv.Close()

	roots, err := observe(t, models.Step{Name: "ping", Request: models.Request{Method: "GET", URL: srv.URL}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}

	got := keys(roots)
	want := append([]string{}, check.Roots(check.API)...)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("observed roots = %v, want check.Roots(check.API) = %v", got, want)
	}
}

// status is a number in the evaluator's domain, so `expect status == 200`
// compares two float64s and the report does not annotate the two sides with
// disagreeing types.
func TestObserveStatusIsANumber(t *testing.T) {
	srv := httptest.NewServer(jsonAt(http.StatusTeapot, `{}`))
	defer srv.Close()

	roots, err := observe(t, models.Step{Request: models.Request{Method: "GET", URL: srv.URL}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if got, ok := roots[RootStatus].(float64); !ok || got != 418 {
		t.Errorf("roots[status] = %#v, want float64(418)", roots[RootStatus])
	}
}

func TestObserveBodyAndRaw(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantBody any
	}{
		{"an object", `{"id": 42}`, map[string]any{"id": float64(42)}},
		{"an array", `[1, 2]`, []any{float64(1), float64(2)}},
		{"not json", "moved to /items/42\n", nil},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(jsonAt(http.StatusOK, c.body))
			defer srv.Close()

			roots, err := observe(t, models.Step{Request: models.Request{Method: "GET", URL: srv.URL}})
			if err != nil {
				t.Fatalf("Observe() error = %v", err)
			}
			if got := fmt.Sprint(roots[RootBody]); got != fmt.Sprint(c.wantBody) {
				t.Errorf("roots[body] = %#v, want %#v", roots[RootBody], c.wantBody)
			}
			// raw is every byte whether or not it parsed: a capture off a
			// plain-text response reads it through match().
			if got := roots[RootRaw]; got != c.body {
				t.Errorf("roots[raw] = %q, want %q", got, c.body)
			}
		})
	}
}

// headers is an eval.Headers, so a name's case does not matter, and a repeated
// header is one comma-joined string rather than sometimes an array.
func TestObserveHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	roots, err := observe(t, models.Step{Request: models.Request{Method: "GET", URL: srv.URL}})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	headers, ok := roots[RootHeaders].(eval.Headers)
	if !ok {
		t.Fatalf("roots[headers] is %T, want eval.Headers", roots[RootHeaders])
	}
	if got := headers["Content-Type"]; got != "application/json" {
		t.Errorf("headers[Content-Type] = %v, want application/json", got)
	}
	if got := headers["Set-Cookie"]; got != "a=1, b=2" {
		t.Errorf("headers[Set-Cookie] = %v, want \"a=1, b=2\"", got)
	}
}

// The step arrives rendered, so a brace in a URL or a body is a brace. Running
// the {{}} substituter over it -- which Execute does, correctly, for a YAML
// step -- would reinterpret it.
func TestObserveDoesNotTemplateTheStep(t *testing.T) {
	var gotBody string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	step := models.Step{Request: models.Request{
		Method: "POST",
		URL:    srv.URL + "/items?q=%7B%7Bnope%7D%7D",
		Body:   `{"template": "{{nope}}"}`,
	}}
	if _, err := observe(t, step); err != nil {
		t.Fatalf("Observe() error = %v -- a brace is not a placeholder here", err)
	}
	if gotBody != `{"template": "{{nope}}"}` {
		t.Errorf("body sent = %q, want it verbatim", gotBody)
	}
	if gotPath != "/items?q=%7B%7Bnope%7D%7D" {
		t.Errorf("path sent = %q, want it verbatim", gotPath)
	}
}

// A wrong status is an observation, not an error: what the scenario thinks of
// it is decided by its expects, and nothing here is special about the status.
func TestObserveDoesNotJudgeTheStatus(t *testing.T) {
	srv := httptest.NewServer(jsonAt(http.StatusInternalServerError, `{"error": "boom"}`))
	defer srv.Close()

	step := models.Step{
		Request:  models.Request{Method: "GET", URL: srv.URL},
		Response: models.Response{StatusCode: 200},
	}
	roots, err := observe(t, step)
	if err != nil {
		t.Fatalf("Observe() error = %v, want nil: a 500 is an answer", err)
	}
	if roots[RootStatus] != float64(500) {
		t.Errorf("roots[status] = %v, want 500", roots[RootStatus])
	}
	// The body of an unexpected status is still observed, which is the rule the
	// DSL changes: Execute suppresses the body checks, Observe reports the body.
	if roots[RootBody] == nil {
		t.Error("roots[body] = nil, want the 500's body: a .art step asserts on it itself")
	}
}

// A step's own timeout is honoured, and the message names the scenario's number.
func TestObserveRespectsTheStepTimeout(t *testing.T) {
	// The handler answers only when the client gives up, so Close does not
	// block on an outstanding request at the end of the test.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	step := models.Step{
		Timeout: "20ms",
		Request: models.Request{Method: "GET", URL: srv.URL},
	}
	_, err := observe(t, step)
	if err == nil {
		t.Fatal("Observe() error = nil, want a timeout")
	}
	if want := "no response within 20ms"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// A connection nobody is listening on is a step that could not run.
func TestObserveTransportFailureIsAnError(t *testing.T) {
	_, err := observe(t, models.Step{Request: models.Request{Method: "GET", URL: "http://127.0.0.1:1/ping"}})
	if err == nil {
		t.Fatal("Observe() error = nil, want a transport failure")
	}
}

func observe(t *testing.T, step models.Step) (map[string]any, error) {
	t.Helper()
	return Executor{}.Observe(context.Background(), step, executor.NewScope())
}

func jsonAt(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
