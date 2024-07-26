package models

type Variable struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type BodyCheck struct {
	Path  string `yaml:"path"`
	Value string `yaml:"value"`
	Type  string `yaml:"type"`
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
