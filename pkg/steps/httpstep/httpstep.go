// Package httpstep is the HTTP step: it sends a request and reports what came
// back. It is registered in the default registry under the step type "api",
// which is what pkg/dsl/lower stamps on a step whose action block is an HTTP
// verb -- the package is named for the protocol, the type for the registry.
//
// It makes no assertions. What a scenario expects of a response is an `expect`
// expression, evaluated by pkg/eval against the roots Observe returns, which
// is why a wrong status no longer suppresses the checks after it (ART-40).
//
// It is the only place in artemis where an *http.Response exists. Nothing it
// returns holds a body, so a body cannot be left open by a caller: before this
// package, CallAPI handed a live response to pkg/cli and the runner had to
// remember to drain and close it, with a //nolint:bodyclose to quiet the linter
// about the hand-off.
//
// Per ART-15's contract: one attempt per Observe, no retrying, no timing.
package httpstep

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// StepType is the type a scenario writes to get this executor.
const StepType = "api"

// DefaultTimeout is how long one attempt may take when the step does not say.
// There is no "no timeout": a request with no deadline is how a CI job hangs
// until someone notices, and 30s is far above any API worth asserting on.
const DefaultTimeout = 30 * time.Second

// client is shared by every step. Its Timeout field is deliberately unset --
// the deadline is a context per attempt, so each step can have its own while
// still reusing connections. A client per call, which is what pkg/shared/api
// did, threw the connection pool away on every request.
var client = &http.Client{}

// Executor is the HTTP step.
type Executor struct {
	// Client sends the requests. Zero value means the package's shared client.
	Client *http.Client
	// Timeout is the deadline for a step that does not set one. Zero means
	// DefaultTimeout.
	Timeout time.Duration
}

func init() {
	executor.Register(StepType, Executor{})
}

// response is what one exchange produced: everything the two front ends read
// off an HTTP answer, and no live body. Nothing this package returns holds an
// open response, which is why a caller cannot leak one.
type response struct {
	status  int
	headers http.Header
	body    []byte
}

// exchange sends req, reads the whole answer and closes it.
//
// Observe is its only caller. It stays a function of its own because the
// deadline wording, the draining and the logging are the parts worth having in
// one place when the browser step arrives.
func (e Executor) exchange(ctx context.Context, step models.Step, req *http.Request, timeout time.Duration) (response, error) {
	start := time.Now()
	resp, err := e.client().Do(req)
	if err != nil {
		// The deadline is the scenario's own number, so say which one was hit
		// rather than leaving the reader to recognise "context deadline
		// exceeded".
		if ctx.Err() == context.DeadlineExceeded {
			return response{}, fmt.Errorf("no response within %s: %w", timeout, err)
		}
		return response{}, fmt.Errorf("performing request: %w", err)
	}
	// Read to EOF before closing: a body left part-read is a connection that is
	// not reused, which with retries is a leak per attempt rather than per step.
	body, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		logger.Logger.Warn("Error closing response body", "name", step.Name, "error", closeErr.Error())
	}
	logger.Logger.Info("API response", "name", step.Name, "status", resp.StatusCode, "time", time.Since(start))
	if readErr != nil {
		return response{}, fmt.Errorf("reading response body: %w", readErr)
	}
	return response{status: resp.StatusCode, headers: resp.Header, body: body}, nil
}

func (e Executor) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return client
}

func (e Executor) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}
