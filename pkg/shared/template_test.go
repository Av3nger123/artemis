package shared

import (
	"encoding/json"
	"strings"
	"testing"
)

// cfg is the config map the templater resolves against: strings as scenario
// variables write them, and the shapes encoding/json produces for a capture.
func cfg() map[string]interface{} {
	return map[string]interface{}{
		"url":      "https://api.example.com",
		"id":       float64(42),
		"ratio":    3.5,
		"count":    7,
		"ok":       true,
		"missing":  nil,
		"user":     map[string]interface{}{"name": "ada"},
		"tags":     []interface{}{"a", float64(2)},
		"template": "{{url}}",
		"number":   json.Number("1234567890123456789"),
	}
}

func TestTransformTextRenders(t *testing.T) {
	cases := []struct {
		name     string
		template string
		want     string
	}{
		{"empty", "", ""},
		{"plain text", "nothing to see", "nothing to see"},
		{"single placeholder", "{{url}}/token", "https://api.example.com/token"},
		{"repeated placeholder", "{{url}} and {{url}}", "https://api.example.com and https://api.example.com"},
		{"adjacent placeholders", "{{url}}{{id}}", "https://api.example.com42"},
		{"spaces inside", "{{ url }}/token", "https://api.example.com/token"},
		{"integral float", "/users/{{id}}", "/users/42"},
		{"fractional float", "r={{ratio}}", "r=3.5"},
		{"int", "n={{count}}", "n=7"},
		{"bool", "ok={{ok}}", "ok=true"},
		{"json.Number keeps its digits", "n={{number}}", "n=1234567890123456789"},
		{"nil is null", `{"v": {{missing}}}`, `{"v": null}`},
		{"object is compact json", `{"user": {{user}}}`, `{"user": {"name":"ada"}}`},
		{"array is compact json", `{"tags": {{tags}}}`, `{"tags": ["a",2]}`},
		{"lone brace", "a{b", "a{b"},
		{"trailing brace", "a{", "a{"},
		{"only a brace", "{", "{"},
		{"close with no open", "a}}b", "a}}b"},
		{"brace then space then placeholder", "{ {{url}}", "{ https://api.example.com"},
		{"substituted value is not re-expanded", "{{template}}", "{{url}}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := TransformText(c.template, cfg())
			if err != nil {
				t.Fatalf("TransformText(%q) errored: %v", c.template, err)
			}
			if got != c.want {
				t.Errorf("TransformText(%q) = %q, want %q", c.template, got, c.want)
			}
		})
	}
}

func TestTransformTextErrors(t *testing.T) {
	cases := []struct {
		name     string
		template string
		wantIn   []string
	}{
		{"unclosed at end", "{{token", []string{"unclosed", "{{token"}},
		{"one closing brace", "{{url}", []string{"unclosed", "{{url}"}},
		{"unclosed after text", "a/{{ url", []string{"unclosed", "offset 2"}},
		{"second placeholder unclosed", "{{url}}/{{token", []string{"unclosed", "{{token"}},
		{"empty placeholder", "{{}}", []string{"empty placeholder"}},
		{"blank placeholder", "{{   }}", []string{"empty placeholder"}},
		{"unknown name", "{{tokn}}/x", []string{"unknown variable", "tokn"}},
		{"unknown name after a known one", "{{url}}/{{tokn}}", []string{"unknown variable", "tokn", "offset 8"}},
		{"a third brace is part of the name", "{{{url}}", []string{"unknown variable", "{url"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := TransformText(c.template, cfg())
			if err == nil {
				t.Fatalf("TransformText(%q) = %q, want an error", c.template, got)
			}
			for _, want := range c.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// A nil map is what a scenario with no variables and no captures has. Looking a
// name up in it must be an unknown variable, not a panic.
func TestTransformTextNilConfig(t *testing.T) {
	got, err := TransformText("a{", nil)
	if err != nil || got != "a{" {
		t.Fatalf(`TransformText("a{", nil) = %q, %v; want "a{", nil`, got, err)
	}
	if _, err := TransformText("{{url}}", nil); err == nil {
		t.Error("want an unknown-variable error against a nil config")
	}
}

// Every byte-length prefix of a template full of delimiters either renders or
// errors -- the point is that none of them panics.
func TestTransformTextNeverPanicsOnTruncation(t *testing.T) {
	full := "a{{url}}b{{ id }}c{{}}d{{tokn}}e{{{}}}f}}"
	for i := 0; i <= len(full); i++ {
		prefix := full[:i]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("TransformText(%q) panicked: %v", prefix, r)
				}
			}()
			_, _ = TransformText(prefix, cfg())
		}()
	}
}
