// Package capture applies a step's `capture:` map to whatever the step produced.
//
// It belongs to no step type. An executor hands it the raw output and, when it
// has one, that output decoded as a JSON object; it reads each value, writes it
// into the scope the scenario carries, and hands back an errored assertion for
// every value it could not read. That is the whole of the arrangement, so the
// exec, db and browser steps get captures by filling in a Source rather than by
// writing any of this again.
//
// It is the counterpart of pkg/shared/assert: given a step and what it produced,
// return assertions. It deliberately does not import pkg/executor -- Apply takes
// the plain map an executor.Scope already is -- so the dependency runs one way.
package capture

import (
	"errors"
	"fmt"
	"strings"

	"github.com/oliveagle/jsonpath"

	"artemis/pkg/result"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// Kind is the assertion kind an unreadable capture is recorded under.
const Kind = "capture"

// ErrNoJSON is what a JSON-path capture from output that was not a JSON object
// reports. It is a package-level error so a test, or a step type, can match it
// rather than its wording.
var ErrNoJSON = errors.New("no parsed JSON output to capture from")

// Source is what a step produced, in the two forms a capture can read.
//
// Text is the output as it arrived -- an HTTP response body, a command's stdout
// -- and is what a regex is matched against. JSON is that same output decoded as
// a JSON object, or nil when it was not one; a JSON-path capture needs it and
// says so when it is missing. A step type that produces no text at all leaves
// Text empty, and its regex captures fail for the honest reason that there was
// nothing to match.
type Source struct {
	Text []byte
	JSON map[string]any
}

// Apply reads every value the step captures and writes it into vars.
//
// Captures are read in sorted key order, so the assertions a failed step prints
// come out the same on every run. A capture that succeeds records no assertion:
// a capture is plumbing, not a check, and a run that printed one line per
// captured token would bury the checks that matter. A capture that fails is one
// errored assertion naming the key, and the rest are still attempted -- two
// mistyped paths should take one run to find, not two.
//
// A failed capture writes nothing, so a later step referencing it fails on an
// unknown variable rather than on a value that is quietly wrong.
func Apply(step models.Step, src Source, vars map[string]any) []result.AssertionResult {
	keys := step.CaptureKeys()
	if len(keys) == 0 {
		return nil
	}

	var out []result.AssertionResult
	for _, key := range keys {
		c := step.Capture[key]
		val, err := read(c, src)
		if err != nil {
			// The cause is kept: "there was no JSON here" and "that path is
			// not in this JSON" are different mistakes with different fixes.
			logger.Logger.Warn("Error while capturing a value", "name", step.Name, "key", key, "error", err.Error())
			out = append(out, assertionFor(step, key, c).Errored(err))
			continue
		}
		// A captured value goes to the log, never to stdout: it is often a
		// token, and the terminal belongs to the step list.
		logger.Logger.Debug("Captured value", "name", step.Name, "key", key)
		vars[key] = val
	}
	return out
}

// assertionFor describes the capture an errored assertion is about: the key in
// Path, because that is the name a scenario would go and fix, and the source
// kind as the operator.
func assertionFor(step models.Step, key string, c models.Capture) result.Assertion {
	operator := "json"
	expected := any(c.JSON)
	if c.Regex != "" {
		operator, expected = "regex", any(c.Regex)
	}
	return result.Assertion{
		Step:     step.Name,
		Kind:     Kind,
		Path:     key,
		Operator: operator,
		Expected: expected,
	}
}

// read returns the value c names in src.
func read(c models.Capture, src Source) (any, error) {
	switch {
	case c.Regex != "":
		return readRegex(c, src.Text)
	case strings.TrimSpace(c.JSON) != "":
		return readJSON(c.JSON, src.JSON)
	default:
		// models.Config.Validate rejects this before anything runs; a Capture
		// built in Go can still reach here.
		return nil, errors.New("capture gives no json or regex to read the value with")
	}
}

// readJSON reads the value at a JSON path out of decoded output.
//
// A panic from the jsonpath library is turned into an error: a path the library
// chokes on must fail its step with a reason rather than taking the whole run
// down with a stack trace. The value keeps the type encoding/json gave it.
func readJSON(path string, data map[string]any) (val any, err error) {
	if data == nil {
		return nil, ErrNoJSON
	}
	defer func() {
		if r := recover(); r != nil {
			val, err = nil, fmt.Errorf("path %q could not be read: %v", path, r)
		}
	}()

	val, err = jsonpath.JsonPathLookup(data, path)
	if err != nil {
		return nil, fmt.Errorf("path %q not found in the output: %w", path, err)
	}
	return val, nil
}

// readRegex matches the pattern against the output as plain text.
//
// The value is capturing group 1 when the pattern has a group, and the whole
// match when it does not, so the common `id=([0-9]+)` needs no second knob to
// say which part was wanted. It is always a string: the text that matched, not a
// guess at what it meant.
func readRegex(c models.Capture, text []byte) (any, error) {
	re, err := c.Pattern()
	if err != nil {
		return nil, err
	}
	m := re.FindSubmatch(text)
	if m == nil {
		return nil, fmt.Errorf("regex %q matched nothing in the output", c.Regex)
	}
	if re.NumSubexp() > 0 {
		return string(m[1]), nil
	}
	return string(m[0]), nil
}
