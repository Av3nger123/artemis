package httpstep

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// scope is the variable map a step is handed. A rendered step reads nothing
// out of it -- pkg/dsl/lower resolved every expression before Observe is
// called -- but Observer's contract passes one, so the tests pass a real one.
func scope() executor.Scope {
	return executor.ScopeOf(map[string]any{
		"base":  "",
		"token": "sekret",
		"id":    float64(7),
	})
}

// step is a rendered api step. There is no expected status: what a scenario
// expects of a response is an `expect` expression now, evaluated by the runner
// against what Observe reports (ART-40).
func step(name string, r models.Request) models.Step {
	return models.Step{Name: name, Type: StepType, Request: r}
}

// observeWith runs the step with a short default timeout, so a test that
// accidentally waits on a server does not wait DefaultTimeout.
func observeWith(step models.Step, sc executor.Scope) (map[string]any, error) {
	return Executor{Timeout: 2 * time.Second}.Observe(context.Background(), step, sc)
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

func TestObserveRejectsAnUnusableRequest(t *testing.T) {
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
			if _, err := observeWith(step("s", c.request), scope()); err == nil {
				t.Fatal("Observe() = nil error, want a failure")
			} else if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantIn)
			}
		})
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

	s := step("hang", models.Request{URL: srv.URL, Method: http.MethodGet})
	s.Timeout = "50ms"

	start := time.Now()
	res, err := Executor{Timeout: time.Hour}.Observe(context.Background(), s, scope())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Observe() = nil error, want the deadline")
	}
	if res != nil {
		t.Errorf("Observe() = %#v alongside the error, want nil", res)
	}
	if !strings.Contains(err.Error(), "no response within 50ms") {
		t.Errorf("error = %q, want it to name the timeout that was hit", err)
	}
	// Generous: the point is that the step's 50ms won over the executor's hour.
	if elapsed > 10*time.Second {
		t.Errorf("Observe() took %v, want it bounded by the step's 50ms", elapsed)
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

	s := step("hang", models.Request{URL: srv.URL, Method: http.MethodGet})
	if s.Timeout != "" {
		t.Fatal("test is broken: the step must not set a timeout")
	}

	if _, err := (Executor{Timeout: 50 * time.Millisecond}).Observe(context.Background(), s, scope()); err == nil {
		t.Fatal("Observe() = nil error, want the default deadline to have been applied")
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

	s := step("ping", models.Request{URL: srv.URL, Method: http.MethodGet})
	s.Timeout = "soon"

	if _, err := observeWith(s, scope()); err == nil {
		t.Fatal("Observe() = nil error, want the bad timeout")
	} else if !strings.Contains(err.Error(), `timeout "soon"`) {
		t.Errorf("error = %q, want it to name the timeout", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the server saw %d requests, want 0", n)
	}
}

// The body is drained and closed inside exchange, so the connection goes back to
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
	s := step("ping", models.Request{URL: srv.URL, Method: http.MethodGet})
	for i := 0; i < 3; i++ {
		if _, err := e.Observe(context.Background(), s, scope()); err != nil {
			t.Fatalf("Observe() %d = %v, want nil", i, err)
		}
	}

	if n := atomic.LoadInt32(&conns); n != 1 {
		t.Errorf("the server saw %d connections for 3 requests, want 1 -- a body that is not drained and closed is a connection that is not reused", n)
	}
}
