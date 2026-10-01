package models

import (
	"fmt"
	"time"
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
func (r *Retry) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var times int
	if err := unmarshal(&times); err == nil {
		r.Times, r.Delay = times, ""
		return nil
	}

	// A mapping. The alias avoids recursing back into this method.
	type retry Retry
	var full retry
	if err := unmarshal(&full); err != nil {
		return fmt.Errorf("retry must be a number of attempts or a {times, delay} mapping: %w", err)
	}
	*r = Retry(full)
	return nil
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
