package httpstep

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// scope is the variable map a request is rendered against.
func scope() executor.Scope {
	return executor.ScopeOf(map[string]any{
		"base":  "",
		"token": "sekret",
		"id":    float64(7),
	})
}

func step(name string, r models.Request, wantStatus int) models.Step {
	return models.Step{Name: name, Type: StepType, Request: r, Response: models.Response{StatusCode: wantStatus}}
}

// run executes the step with a short default timeout, so a test that
// accidentally waits on a server does not wait DefaultTimeout.
func run(step models.Step, sc executor.Scope) (*result.StepResult, error) {
	return Executor{Timeout: 2 * time.Second}.Execute(step, sc)
}

// serve answers every request with the given status and body.
func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The registration is the whole point of the package: a scenario writing
// `type: api` has to reach this executor through the default registry.
func TestItIsRegisteredAsAPI(t *testing.T) {
	e, ok := executor.Default().Lookup(StepType)
	if !ok {
		t.Fatalf("nothing is registered for %q", StepType)
	}
	if _, isHTTP := e.(Executor); !isHTTP {
		t.Errorf("%q is registered to %T, want httpstep.Executor", StepType, e)
	}
	var found bool
	for _, typ := range executor.Default().Types() {
		if typ == StepType {
			found = true
		}
	}
	if !found {
		t.Errorf("Types() = %v, want it to contain %q -- Validate reads this list", executor.Default().Types(), StepType)
	}
}

func TestExecuteSendsTheRenderedRequest(t *testing.T) {
	var got struct {
		method, path, header, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.header, got.body = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sc := scope()
	sc.Set("base", srv.URL)

	res, err := run(step("create", models.Request{
		URL:     "{{base}}/items/{{id}}",
		Method:  http.MethodPost,
		Headers: map[string]string{"Authorization": "Bearer {{token}}"},
		Body:    `{"id": {{id}}}`,
	}, 200), sc)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !result.AllPassed(res.Assertions) {
		t.Errorf("assertions = %v, want all passed", res.Assertions)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/items/7" {
		t.Errorf("path = %q, want /items/7 -- the captured id should render without a decimal point", got.path)
	}
	if got.header != "Bearer sekret" {
		t.Errorf("Authorization = %q, want %q", got.header, "Bearer sekret")
	}
	if got.body != `{"id": 7}` {
		t.Errorf("body = %q, want %q", got.body, `{"id": 7}`)
	}
}

// Every placeholder in a request is rendered before anything is sent, and a
// placeholder that cannot be rendered says which part of the request it was in.
func TestRenderErrorsNameWhatFailedToRender(t *testing.T) {
	cases := []struct {
		name    string
		request models.Request
		wantIn  string
	}{
		{"url", models.Request{URL: "{{nope}}/x", Method: http.MethodGet}, "rendering request url"},
		{"body", models.Request{URL: "http://127.0.0.1:1", Method: http.MethodGet, Body: "{{nope}}"}, "rendering request body"},
		{
			"header",
			models.Request{URL: "http://127.0.0.1:1", Method: http.MethodGet, Headers: map[string]string{"X-Token": "{{nope}}"}},
			`rendering header "X-Token"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := run(step("s", c.request, 200), scope())
			if err == nil {
				t.Fatal("Execute() = nil error, want a failure")
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantIn)
			}
			if res != nil {
				t.Errorf("Execute() = %#v alongside the error, want nil", res)
			}
		})
	}
}

func TestExecuteRejectsAnUnusableRequest(t *testing.T) {
	cases := []struct {
		name    string
		request models.Request
		wantIn  string
	}{
		{"bad method", models.Request{URL: "http://127.0.0.1:1", Method: "GET POST"}, "creating request"},
		{"no scheme", models.Request{URL: "127.0.0.1/x", Method: http.MethodGet}, "performing request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := run(step("s", c.request, 200), scope()); err == nil {
				t.Fatal("Execute() = nil error, want a failure")
			} else if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantIn)
			}
		})
	}
}

// The status assertion is the one check every step makes, whether or not it
// asked for others, and it comes first because that is the order it prints in.
func TestStatusAssertionIsAlwaysFirst(t *testing.T) {
	srv := serve(t, http.StatusCreated, `{"id": 1}`)

	s := step("create", models.Request{URL: srv.URL, Method: http.MethodPost}, http.StatusCreated)
	s.Response.Body = []models.BodyCheck{{Path: "$.id", Operator: "exists"}}

	res, err := run(s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if len(res.Assertions) != 2 {
		t.Fatalf("got %d assertions, want 2: %v", len(res.Assertions), res.Assertions)
	}
	if res.Assertions[0].Kind != "status_code" {
		t.Errorf("first assertion = %q, want status_code", res.Assertions[0].Kind)
	}
	if res.Assertions[0].Step != "create" {
		t.Errorf("assertion step = %q, want the step's name", res.Assertions[0].Step)
	}
}

// A 500's body is not the body the scenario described, so asserting against it
// would bury the one line that matters under a dozen that do not.
func TestWrongStatusIsTheOnlyAssertion(t *testing.T) {
	srv := serve(t, http.StatusInternalServerError, `{"status": "boom"}`)

	s := step("ping", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
	s.Response.Body = []models.BodyCheck{{Path: "$.status", Value: "ok"}}
	s.Scripts = []models.Script{{Key: "k", Path: "$.status"}}

	sc := scope()
	res, err := run(s, sc)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil -- a wrong status is a failed assertion, not an error", err)
	}
	if len(res.Assertions) != 1 {
		t.Fatalf("got %d assertions, want only the status one: %v", len(res.Assertions), res.Assertions)
	}
	if res.Assertions[0].Passed() {
		t.Error("the status assertion passed, want fail")
	}
	if _, ok := sc.Get("k"); ok {
		t.Error("a step that got the wrong status still captured")
	}
}

func TestBodyChecksBecomeOneAssertionEach(t *testing.T) {
	srv := serve(t, 200, `{"status": "ok", "count": 3}`)

	s := step("ping", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
	s.Response.Body = []models.BodyCheck{
		{Path: "$.status", Value: "ok"},
		{Path: "$.count", Value: 2},
	}

	res, err := run(s, scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if len(res.Assertions) != 3 {
		t.Fatalf("got %d assertions, want status + one per check: %v", len(res.Assertions), res.Assertions)
	}
	if !res.Assertions[1].Passed() {
		t.Errorf("$.status assertion = %s, want pass", res.Assertions[1].Status)
	}
	if res.Assertions[2].Passed() {
		t.Errorf("$.count assertion = %s, want fail", res.Assertions[2].Status)
	}
}

func TestCapturesLandInTheScope(t *testing.T) {
	srv := serve(t, 200, `{"token": "abc", "user": {"id": 7}, "items": [{"sku": "x1"}]}`)

	s := step("login", models.Request{URL: srv.URL, Method: http.MethodPost}, 200)
	s.Scripts = []models.Script{
		{Key: "authToken", Path: "$.token"},
		{Key: "userId", Path: "$.user.id"},
		{Key: "sku", Path: "$.items[0].sku"},
	}

	sc := scope()
	res, err := run(s, sc)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !result.AllPassed(res.Assertions) {
		t.Errorf("assertions = %v, want all passed", res.Assertions)
	}
	for key, want := range map[string]any{"authToken": "abc", "userId": float64(7), "sku": "x1"} {
		if got, ok := sc.Get(key); !ok || got != want {
			t.Errorf("scope[%q] = %#v, %v; want %#v, true", key, got, ok, want)
		}
	}
	// Variables that were already there are untouched.
	if got, _ := sc.Get("token"); got != "sekret" {
		t.Errorf("scope[%q] = %#v, want it left alone", "token", got)
	}
}

func TestCaptureThatCannotBeMadeIsAnErroredAssertion(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		path   string
		wantIn string
	}{
		{"path does not resolve", `{"token": "abc"}`, "$.nope.deeper", "$.nope.deeper"},
		{"no path given", `{"token": "abc"}`, "", "no path given"},
		{"body is not json", `<html>no</html>`, "$.token", "no parsed response body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := serve(t, 200, c.body)
			s := step("login", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
			s.Scripts = []models.Script{{Key: "authToken", Path: c.path}}

			sc := scope()
			res, err := run(s, sc)
			if err != nil {
				t.Fatalf("Execute() = %v, want nil -- a bad capture is an errored assertion, not a step that could not run", err)
			}
			last := res.Assertions[len(res.Assertions)-1]
			if last.Kind != "capture" || last.Status != result.StatusError {
				t.Fatalf("last assertion = %+v, want an errored capture", last)
			}
			if !strings.Contains(last.Error, c.wantIn) {
				t.Errorf("assertion error = %q, want it to mention %q", last.Error, c.wantIn)
			}
			if _, ok := sc.Get("authToken"); ok {
				t.Error("a failed capture still wrote to the scope")
			}
		})
	}
}

// A body that is not JSON is only a problem for a step that needed one.
func TestANonJSONBodyWithNothingToReadFromItPasses(t *testing.T) {
	srv := serve(t, 204, ``)

	res, err := run(step("delete", models.Request{URL: srv.URL, Method: http.MethodDelete}, 204), scope())
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !result.AllPassed(res.Assertions) {
		t.Errorf("assertions = %v, want all passed -- nothing was asked of the body", res.Assertions)
	}
}

// The step's own timeout is what bounds an attempt, and the error names it: a
// bare "context deadline exceeded" leaves the reader to guess which number was
// hit.
func TestTheStepsTimeoutBoundsTheAttempt(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	s := step("hang", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
	s.Timeout = "50ms"

	start := time.Now()
	res, err := Executor{Timeout: time.Hour}.Execute(s, scope())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Execute() = nil error, want the deadline")
	}
	if res != nil {
		t.Errorf("Execute() = %#v alongside the error, want nil", res)
	}
	if !strings.Contains(err.Error(), "no response within 50ms") {
		t.Errorf("error = %q, want it to name the timeout that was hit", err)
	}
	// Generous: the point is that the step's 50ms won over the executor's hour.
	if elapsed > 10*time.Second {
		t.Errorf("Execute() took %v, want it bounded by the step's 50ms", elapsed)
	}
}

// With no timeout: on the step, the executor's default applies -- there is no
// path that sends a request with no deadline at all.
func TestTheDefaultTimeoutAppliesWhenTheStepIsSilent(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	s := step("hang", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
	if s.Timeout != "" {
		t.Fatal("test is broken: the step must not set a timeout")
	}

	if _, err := (Executor{Timeout: 50 * time.Millisecond}).Execute(s, scope()); err == nil {
		t.Fatal("Execute() = nil error, want the default deadline to have been applied")
	}
}

// The default is a real number, not zero, so the zero-value Executor the init
// registers cannot send a request without a deadline.
func TestTheZeroExecutorHasADeadline(t *testing.T) {
	if got := (Executor{}).timeout(); got != DefaultTimeout {
		t.Errorf("zero Executor timeout = %v, want DefaultTimeout %v", got, DefaultTimeout)
	}
	if DefaultTimeout <= 0 {
		t.Errorf("DefaultTimeout = %v, want a positive deadline", DefaultTimeout)
	}
	if client.Timeout != 0 {
		t.Error("the shared client sets Timeout; the deadline is meant to be per attempt, from the step")
	}
}

// A timeout that will not parse fails the step before anything is sent.
func TestABadTimeoutIsAnErrorBeforeTheRequest(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	t.Cleanup(srv.Close)

	s := step("ping", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
	s.Timeout = "soon"

	if _, err := run(s, scope()); err == nil {
		t.Fatal("Execute() = nil error, want the bad timeout")
	} else if !strings.Contains(err.Error(), `timeout "soon"`) {
		t.Errorf("error = %q, want it to name the timeout", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the server saw %d requests, want 0", n)
	}
}

// The body is drained and closed inside Execute, so the connection goes back to
// the pool. Two steps in a row against the same server must reuse one
// connection; before this package the caller was responsible for the draining
// and the response escaped the function to make it possible.
func TestTheConnectionIsReused(t *testing.T) {
	var conns int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status": "ok"}`)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&conns, 1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	// One client for both calls, as the shared one is for a whole run.
	e := Executor{Client: &http.Client{}, Timeout: 2 * time.Second}
	s := step("ping", models.Request{URL: srv.URL, Method: http.MethodGet}, 200)
	for i := 0; i < 3; i++ {
		if _, err := e.Execute(s, scope()); err != nil {
			t.Fatalf("Execute() %d = %v, want nil", i, err)
		}
	}

	if n := atomic.LoadInt32(&conns); n != 1 {
		t.Errorf("the server saw %d connections for 3 requests, want 1 -- a body that is not drained and closed is a connection that is not reused", n)
	}
}

// errNoParsedBody is matched by a test rather than by its wording, so the
// message can be reworded without breaking anything.
func TestNoParsedBodyIsASentinel(t *testing.T) {
	if !errors.Is(errNoParsedBody, errNoParsedBody) {
		t.Fatal("unreachable")
	}
	if errNoParsedBody.Error() == "" {
		t.Error("errNoParsedBody has no message")
	}
}
