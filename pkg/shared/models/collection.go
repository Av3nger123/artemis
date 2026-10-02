package models

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

type Variable struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// BodyCheck is one assertion against a response body.
//
// Value keeps whatever type the YAML scalar had -- `value: 200` is an int,
// `value: "200"` a string, `value: true` a bool -- because the assertion
// engine compares against values that came out of encoding/json, where every
// number is a float64. Operator names the comparison and defaults to "equals";
// Type, when set, names the JSON type the value at Path must have.
type BodyCheck struct {
	Path     string `yaml:"path"`
	Operator string `yaml:"operator,omitempty"`
	Value    any    `yaml:"value"`
	Type     string `yaml:"type,omitempty"`
}

type Request struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`
}

type Response struct {
	StatusCode int         `yaml:"status_code"`
	Body       []BodyCheck `yaml:"body,omitempty"`
}

type Script struct {
	Key  string `yaml:"key"`
	Path string `yaml:"path"`
}

type Step struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Request  Request  `yaml:"request"`
	Response Response `yaml:"response"`
	Scripts  []Script `yaml:"scripts,omitempty"`
	Retry    Retry    `yaml:"retry,omitempty"`
	// Timeout is how long one attempt of this step may take, as a Go duration
	// string ("5s", "1m30s"). It is per attempt, not per step: a step with
	// `retry: {times: 3}` and `timeout: "5s"` may take fifteen seconds. Absent,
	// a default applies -- see AttemptTimeout -- because no deadline at all is
	// how a run hangs until someone kills it.
	Timeout string `yaml:"timeout,omitempty"`
}

// AttemptTimeout is how long one attempt of the step may take, falling back to
// def when the step does not say. A duration that will not parse, or one that is
// negative, is an error: it is the scenario's mistake and silently running
// without a deadline is the one outcome worth refusing.
//
// Zero -- `timeout: "0s"` -- is also def rather than "no deadline". There is no
// spelling of "wait forever", by design.
func (s Step) AttemptTimeout(def time.Duration) (time.Duration, error) {
	if s.Timeout == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil {
		return 0, fmt.Errorf("timeout %q is not a duration (want something like \"500ms\" or \"5s\")", s.Timeout)
	}
	if d < 0 {
		return 0, fmt.Errorf("timeout %q is negative", s.Timeout)
	}
	if d == 0 {
		return def, nil
	}
	return d, nil
}

// CheckTimeout reports whether Timeout is a value an executor can use.
//
// It is what the runner asks before the first attempt, so a `timeout: "soon"`
// costs no requests to discover. The runner has no business naming a default --
// that belongs to the step type -- so the one passed here is inert: only the
// error is read.
func (s Step) CheckTimeout() error {
	_, err := s.AttemptTimeout(time.Second)
	return err
}

// Retry is how many times a step may be attempted and how long to wait between
// attempts.
//
// Times is the total number of attempts, not the number of retries after the
// first: `times: 3` sends at most three requests. An absent, zero or negative
// Times means one attempt -- a step is never attempted zero times. Delay is a
// Go duration string ("500ms", "2s", "1m30s"); absent, there is no sleep at
// all, and whatever its value nothing is slept before the first attempt or
// after the last.
type Retry struct {
	Times int    `yaml:"times,omitempty"`
	Delay string `yaml:"delay,omitempty"`
}

// UnmarshalYAML accepts both `retry: {times: 3, delay: "1s"}` and the older
// scalar `retry: 5`, which means `times: 5`.
//
// The decoder's KnownFields setting does not reach a node decoded by hand, so
// an unknown key in the mapping is rejected here: otherwise `retry:` would be
// the one corner of the file where a typo still vanished.
func (r *Retry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var times int
		if err := node.Decode(&times); err != nil {
			return retryShapeError(node, err)
		}
		r.Times, r.Delay = times, ""
		return nil

	case yaml.MappingNode:
		// Content alternates key, value, key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			switch key := node.Content[i].Value; key {
			case "times", "delay":
			default:
				return fmt.Errorf("line %d: field %s not found in retry (known fields: times, delay)", node.Content[i].Line, key)
			}
		}
		// The alias avoids recursing back into this method.
		type retry Retry
		var full retry
		if err := node.Decode(&full); err != nil {
			return retryShapeError(node, err)
		}
		*r = Retry(full)
		return nil

	default:
		return retryShapeError(node, nil)
	}
}

// retryShapeError says what retry may be, keeping the decoder's own complaint
// when there is one.
func retryShapeError(node *yaml.Node, err error) error {
	const want = "retry must be a number of attempts or a {times, delay} mapping"
	if err != nil {
		return fmt.Errorf("line %d: %s: %w", node.Line, want, err)
	}
	return fmt.Errorf("line %d: %s", node.Line, want)
}

// Attempts is how many times the step may be tried: always at least one.
func (r Retry) Attempts() int {
	if r.Times < 1 {
		return 1
	}
	return r.Times
}

// Wait is how long to sleep between two attempts. An empty Delay is no wait; a
// delay that will not parse, or one that is negative, is an error.
func (r Retry) Wait() (time.Duration, error) {
	if r.Delay == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(r.Delay)
	if err != nil {
		return 0, fmt.Errorf("retry delay %q is not a duration (want something like \"500ms\" or \"2s\")", r.Delay)
	}
	if d < 0 {
		return 0, fmt.Errorf("retry delay %q is negative", r.Delay)
	}
	return d, nil
}

type Config struct {
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"`
	Variables []Variable `yaml:"variables"`
	Steps     []Step     `yaml:"steps"`
}
