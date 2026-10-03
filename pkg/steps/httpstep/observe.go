package httpstep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// The roots an api step binds, which is what `expect` and `capture` in a .art
// api step may name.
//
// They are declared here as constants and compared against check.Roots(API) in
// observe_test.go rather than imported from pkg/dsl/check: a step package that
// imported the checker would make the runtime depend on the front end, which is
// the dependency pkg/executor exists to avoid. The test is what keeps the two
// lists equal, and a root the checker admits and this file never binds is an
// expect that errors at run time for no reason a reader could find.
const (
	// RootStatus is the response's status code, as a number.
	RootStatus = "status"
	// RootBody is the response body decoded as JSON, nil when it is not JSON.
	RootBody = "body"
	// RootRaw is every byte of the body as text, which is what match() reads.
	RootRaw = "raw"
	// RootHeaders is the response's headers, looked up case-insensitively.
	RootHeaders = "headers"
)

var _ executor.Executor = Executor{}

// Observe sends one request and reports what came back, leaving every assertion
// to the caller.
//
// This is Execute's other half for a .art step: the same deadline, the same
// client, the same exchange, and none of the checking. `expect status == 200` is
// the only thing that asserts a status in a .art file, so nothing here is
// special about the status code -- which is the one behaviour the DSL
// deliberately changes, since the YAML path suppresses body checks when the
// status did not match.
//
// The step is already rendered. pkg/dsl/lower evaluated every expression in it
// against the scenario's scope before this was called, so the request is built
// from step.Request verbatim: running pkg/shared's {{}} substituter over it
// would reinterpret a URL or a body that legitimately contains a brace. scope
// is therefore unread -- it is in the signature because Observer's contract is
// Execute's, and the browser step will want it.
func (e Executor) Observe(ctx context.Context, step models.Step, _ executor.Scope) (map[string]any, error) {
	timeout, err := step.AttemptTimeout(e.timeout())
	if err != nil {
		return nil, err
	}

	// The step's deadline hangs off the one the run was given, so cancelling a
	// run stops the requests it has in flight rather than waiting them out.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := rendered(ctx, step)
	if err != nil {
		return nil, err
	}

	resp, err := e.exchange(ctx, step, req, timeout)
	if err != nil {
		return nil, err
	}
	return roots(step, resp), nil
}

// rendered builds the request from a step whose values are already resolved.
//
// It is build without the substituter, and the two are kept apart rather than
// shared with a flag: "render the templates" and "do not render the templates"
// are the whole difference between the two front ends at this point, and a
// boolean parameter would make the one line that matters the easiest to miss.
func rendered(ctx context.Context, step models.Step) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, step.Request.Method, step.Request.URL, bytes.NewBufferString(step.Request.Body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	for key, value := range step.Request.Headers {
		req.Header.Set(key, value)
	}
	logger.Logger.Info("API call", "name", step.Name, "url", step.Request.URL, "method", step.Request.Method, "headers", req.Header, "body", step.Request.Body)
	return req, nil
}

// roots is the observation an api step binds.
//
// Every value is in the evaluator's domain, which is JSON's:
//
//   - status is a float64, not an int, so `expect status == 200` compares two
//     numbers of one type and the report renders `200` rather than `200` beside
//     a type annotation saying the two sides disagree;
//   - body is decoded into `any`, so an array body is readable. Execute's
//     parseBody decodes into map[string]any and gives up on anything else,
//     which was invisible while a YAML `path:` could only address an object;
//   - raw is the body as text whether or not it was JSON. A capture off a
//     plain-text response reads it through match();
//   - headers is an eval.Headers, whose lookup ignores case, so
//     headers["Content-Type"] is what an author writes and it works.
//
// A body that is not JSON is not an error. Whether the step needed one is
// decided by the expects and captures it wrote, and each of those says so
// itself.
func roots(step models.Step, resp response) map[string]any {
	return map[string]any{
		RootStatus:  float64(resp.status),
		RootBody:    decode(step, resp.body),
		RootRaw:     string(resp.body),
		RootHeaders: headerValues(resp.headers),
	}
}

// decode reads the body as any JSON value, or returns nil.
func decode(step models.Step, body []byte) any {
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		logger.Logger.Warn("Error parsing response body", "name", step.Name, "error", err.Error())
		return nil
	}
	return parsed
}

// headerValues flattens an http.Header into the object `headers` resolves to.
//
// A repeated header joins with ", ", which is what RFC 9110 says a comma-joined
// field line means and what every HTTP client shows. The alternative -- a header
// whose value is sometimes a string and sometimes an array -- would make
// `headers["set-cookie"] contains "x"` mean two things depending on how many
// cookies the server happened to set.
func headerValues(h http.Header) eval.Headers {
	out := make(eval.Headers, len(h))
	for name, values := range h {
		out[name] = strings.Join(values, ", ")
	}
	return out
}
