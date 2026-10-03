// Package httpstep is the HTTP step: it sends a request, checks the response and
// hands the response to pkg/shared/capture, which pulls the step's `capture:`
// values out of it. It is registered in the default registry under the
// step type "api", which is the spelling scenarios have always written -- the
// package is named for the protocol, the type for the YAML.
//
// It is the only place in artemis where an *http.Response exists. Nothing it
// returns holds a body, so a body cannot be left open by a caller: before this
// package, CallAPI handed a live response to pkg/cli and the runner had to
// remember to drain and close it, with a //nolint:bodyclose to quiet the linter
// about the hand-off.
//
// Per ART-15's contract: one attempt per Execute, no retrying, no timing, and a
// returned result that carries assertions and nothing else.
package httpstep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/assert"
	"artemis/pkg/shared/capture"
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

var _ executor.Executor = Executor{}

func init() {
	executor.Register(StepType, Executor{})
}

// Execute sends one request and reports what came back.
//
// The order the assertions come out in is the order they are printed in: the
// status code first, then the body checks, then the captures. Body checks are
// made only when the status matched -- a 500's body is not the body the scenario
// described, and asserting against it would bury the one line that matters under
// a dozen that do not.
func (e Executor) Execute(ctx context.Context, step models.Step, scope executor.Scope) (*result.StepResult, error) {
	timeout, err := step.AttemptTimeout(e.timeout())
	if err != nil {
		return nil, err
	}

	// The step's deadline hangs off the one the run was given, so cancelling a
	// run stops the requests it has in flight rather than waiting them out.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := e.build(ctx, step, scope)
	if err != nil {
		return nil, err
	}

	resp, err := e.exchange(ctx, step, req, timeout)
	if err != nil {
		return nil, err
	}
	body := resp.body

	res := &result.StepResult{}
	statusOK := resp.status == step.Response.StatusCode
	res.Assert(statusAssertion(step, resp.status, statusOK))
	if !statusOK {
		// The body of an unexpected status is log material, not terminal
		// output: it would land unterminated in the middle of the step list.
		logger.Logger.Warn("Unexpected response status", "name", step.Name, "status", resp.status, "body", string(body))
		return res, nil
	}

	parsed := parseBody(step, body)
	for _, a := range assert.Body(step, parsed) {
		res.Assert(a)
	}
	for _, a := range capture.Apply(step, capture.Source{Text: body, JSON: parsed}, scope.Vars()) {
		res.Assert(a)
	}
	return res, nil
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
// Both ways of asking this executor come through here -- Execute, which goes on
// to assert against the step's own checks, and Observe, which hands the answer
// back as the roots a .art expression reads -- so the deadline wording, the
// draining and the logging cannot differ between them.
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

// build renders the request out of scope. Every placeholder is resolved before
// anything is sent, and a failure names the part of the request it was in.
func (e Executor) build(ctx context.Context, step models.Step, scope executor.Scope) (*http.Request, error) {
	vars := scope.Vars()

	url, err := shared.TransformText(step.Request.URL, vars)
	if err != nil {
		return nil, fmt.Errorf("rendering request url: %w", err)
	}
	body, err := shared.TransformText(step.Request.Body, vars)
	if err != nil {
		return nil, fmt.Errorf("rendering request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, step.Request.Method, url, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	for key, value := range step.Request.Headers {
		val, err := shared.TransformText(value, vars)
		if err != nil {
			return nil, fmt.Errorf("rendering header %q: %w", key, err)
		}
		req.Header.Set(key, val)
	}

	logger.Logger.Info("API call", "name", step.Name, "url", url, "method", step.Request.Method, "headers", req.Header, "body", body)
	return req, nil
}

// statusAssertion is the one check every HTTP step makes, whether or not it
// asked for any others.
//
// Its line is the `status_code:` the step wrote, which is the line a reader edits
// when the status is not what they asked for. A step that wrote none leaves it at
// zero and the reader is sent to the step instead (ART-12).
func statusAssertion(step models.Step, got int, ok bool) result.AssertionResult {
	a := result.Assertion{
		Step:     step.Name,
		Kind:     "status_code",
		Operator: "equals",
		Expected: step.Response.StatusCode,
		Actual:   got,
		Line:     step.Response.StatusCodeLine,
	}
	if ok {
		return a.Pass()
	}
	return a.Fail()
}

// parseBody decodes the body as a JSON object, or returns nil. A body that is
// not JSON is not an error here: whether the step needed one is decided by the
// checks and captures it declared, and each of those says so itself.
func parseBody(step models.Step, body []byte) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		logger.Logger.Warn("Error parsing response body", "name", step.Name, "error", err.Error())
		return nil
	}
	return parsed
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
