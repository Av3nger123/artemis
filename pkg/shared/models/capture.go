package models

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Capture is one value a step pulls out of whatever it produced, so the steps
// after it can write it as {{name}}. A step's `capture:` is a map from that name
// to one of these.
//
// Exactly one source is given. JSON is a JSON path into the step's output parsed
// as a JSON object -- an API response body, a command that printed JSON. Regex
// is a pattern matched against the output as plain text, for a step type that
// does not produce JSON at all.
//
// A JSON capture keeps the type encoding/json gave the value: a number stays a
// number, an object stays an object, and ART-6's renderValue is what makes that
// safe to put back into a template. A regex capture is always a string -- the
// text that matched -- because guessing that "007" meant seven loses data.
//
// The compiled pattern is cached on the value when it was decoded from YAML, so
// a regex that will not compile is a load error rather than a surprise on the
// hundredth step. Pattern compiles on demand for a Capture built in Go.
type Capture struct {
	JSON  string `yaml:"json,omitempty"`
	Regex string `yaml:"regex,omitempty"`

	re *regexp.Regexp
}

// captureFields is the keys a capture mapping may have, for the error message
// and for the check that produces it.
var captureFields = []string{"json", "regex"}

// UnmarshalYAML accepts both the shorthand `token: "$.data.token"`, where a
// plain string is a JSON path, and the written-out `token: {json: "$.data.token"}`
// or `token: {regex: "id=([0-9]+)"}`.
//
// Everything that can be known about a capture without running anything is
// decided here, with the YAML line in the message: an unknown key, both sources,
// neither source, an empty source and a regex that will not compile. The
// decoder's KnownFields setting does not reach a node decoded by hand -- the
// same hole models.Retry papers over -- so the key check is written out.
func (c *Capture) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var path string
		if err := node.Decode(&path); err != nil {
			return captureShapeError(node, err)
		}
		*c = Capture{JSON: path}

	case yaml.MappingNode:
		// Content alternates key, value, key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			if !isCaptureField(node.Content[i].Value) {
				return fmt.Errorf("line %d: field %s not found in capture (known fields: %s)",
					node.Content[i].Line, node.Content[i].Value, strings.Join(captureFields, ", "))
			}
		}
		// The alias avoids recursing back into this method.
		type capture Capture
		var full capture
		if err := node.Decode(&full); err != nil {
			return captureShapeError(node, err)
		}
		*c = Capture(full)

	default:
		return captureShapeError(node, nil)
	}

	switch {
	case c.JSON != "" && c.Regex != "":
		return fmt.Errorf("line %d: capture gives both json and regex; it must give exactly one", node.Line)
	case strings.TrimSpace(c.JSON) == "" && strings.TrimSpace(c.Regex) == "":
		return fmt.Errorf("line %d: capture gives no %s to read the value with", node.Line, strings.Join(captureFields, " or "))
	}
	if c.Regex != "" {
		re, err := regexp.Compile(c.Regex)
		if err != nil {
			return fmt.Errorf("line %d: regex %q will not compile: %w", node.Line, c.Regex, err)
		}
		c.re = re
	}
	return nil
}

// Pattern is the compiled Regex. It is the pattern compiled at load time when
// the capture came from YAML, and compiled here when it did not; a capture with
// no Regex returns a nil pattern and no error.
func (c Capture) Pattern() (*regexp.Regexp, error) {
	if c.re != nil || c.Regex == "" {
		return c.re, nil
	}
	re, err := regexp.Compile(c.Regex)
	if err != nil {
		return nil, fmt.Errorf("regex %q will not compile: %w", c.Regex, err)
	}
	return re, nil
}

// CaptureKeys is the names the step captures values under, sorted.
//
// `capture:` is a map, so the order the scenario wrote them in is already gone;
// sorting is what keeps the assertions a failed run prints -- and the order the
// values are read in -- the same on every run.
func (s Step) CaptureKeys() []string {
	if len(s.Capture) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.Capture))
	for k := range s.Capture {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isCaptureField reports whether key is a key a capture mapping may have.
func isCaptureField(key string) bool {
	for _, f := range captureFields {
		if key == f {
			return true
		}
	}
	return false
}

// captureShapeError says what a capture may be, keeping the decoder's own
// complaint when there is one.
func captureShapeError(node *yaml.Node, err error) error {
	const want = "capture must be a JSON path or a {json} or {regex} mapping"
	if err != nil {
		return fmt.Errorf("line %d: %s: %w", node.Line, want, err)
	}
	return fmt.Errorf("line %d: %s", node.Line, want)
}
