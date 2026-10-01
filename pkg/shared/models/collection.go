package models

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
	Retry    int      `yaml:"retry,omitempty"`
}

type Config struct {
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"`
	Variables []Variable `yaml:"variables"`
	Steps     []Step     `yaml:"steps"`
}
