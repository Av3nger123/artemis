package migrate

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeCapture decodes one `capture:` mapping the way the loader does, strict
// fields and all, and returns the step's captures.
func decodeCapture(t *testing.T, body string) (map[string]Capture, error) {
	t.Helper()

	var step Step
	dec := yaml.NewDecoder(strings.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&step); err != nil {
		return nil, err
	}
	return step.Capture, nil
}

func TestAStringCaptureIsAJSONPath(t *testing.T) {
	got, err := decodeCapture(t, "capture:\n  token: \"$.data.access_token\"\n")
	if err != nil {
		t.Fatalf("decode = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("captures = %#v, want one", got)
	}
	if got["token"].JSON != "$.data.access_token" {
		t.Errorf("capture token = %#v, want its JSON path set", got["token"])
	}
	if got["token"].Regex != "" {
		t.Errorf("capture token has a regex %q, want none", got["token"].Regex)
	}
}

func TestTheWrittenOutFormsDecode(t *testing.T) {
	got, err := decodeCapture(t, "capture:\n  a: {json: \"$.x\"}\n  b: {regex: \"id=([0-9]+)\"}\n")
	if err != nil {
		t.Fatalf("decode = %v, want nil", err)
	}
	if got["a"].JSON != "$.x" {
		t.Errorf("capture a = %#v, want json $.x", got["a"])
	}
	if got["b"].Regex != "id=([0-9]+)" {
		t.Errorf("capture b = %#v, want the regex", got["b"])
	}
}

// A regex is compiled while the file is being read, so the pattern an executor
// asks for is already there and a bad one never reaches a request.
func TestARegexIsCompiledAtLoadTime(t *testing.T) {
	got, err := decodeCapture(t, "capture:\n  b: {regex: \"id=([0-9]+)\"}\n")
	if err != nil {
		t.Fatalf("decode = %v, want nil", err)
	}
	if got["b"].re == nil {
		t.Fatal("the decoded capture has no compiled pattern")
	}
	re, err := got["b"].Pattern()
	if err != nil {
		t.Fatalf("Pattern() = %v, want nil", err)
	}
	if re != got["b"].re {
		t.Error("Pattern() recompiled a pattern that was already compiled")
	}
}

// A capture built in Go -- by a test, or by a step type that makes one up --
// still has a usable pattern.
func TestPatternCompilesACaptureThatDidNotComeFromYAML(t *testing.T) {
	re, err := Capture{Regex: "a(b+)"}.Pattern()
	if err != nil {
		t.Fatalf("Pattern() = %v, want nil", err)
	}
	if re == nil || !re.MatchString("abb") {
		t.Errorf("Pattern() = %v, want a pattern matching abb", re)
	}

	re, err = Capture{JSON: "$.x"}.Pattern()
	if err != nil || re != nil {
		t.Errorf("Pattern() = %v, %v; want nil, nil for a json capture", re, err)
	}

	if _, err := (Capture{Regex: "("}).Pattern(); err == nil {
		t.Error("Pattern() = nil error for an uncompilable regex")
	}
}

func TestABadCaptureIsRejectedWhileLoading(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		wantIn string
	}{
		{"unknown key", "capture:\n  t: {jsn: \"$.x\"}\n", "field jsn not found in capture"},
		{"both sources", "capture:\n  t: {json: \"$.x\", regex: \"y\"}\n", "both json and regex"},
		{"neither source", "capture:\n  t: {}\n", "no json or regex"},
		{"empty json path", "capture:\n  t: \"\"\n", "no json or regex"},
		{"blank json path", "capture:\n  t: \"   \"\n", "no json or regex"},
		{"empty regex", "capture:\n  t: {regex: \"\"}\n", "no json or regex"},
		{"regex will not compile", "capture:\n  t: {regex: \"id=([0-9\"}\n", "will not compile"},
		{"a sequence", "capture:\n  t: [\"$.x\"]\n", "capture must be a JSON path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeCapture(t, c.body)
			if err == nil {
				t.Fatalf("decode = %#v, want an error", got)
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("decode = %q, want it to mention %q", err, c.wantIn)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("decode = %q, want the YAML line in it", err)
			}
		})
	}
}

// The field is gone, not renamed to something that still silently accepts the
// old spelling.
func TestScriptsNoLongerParses(t *testing.T) {
	_, err := decodeCapture(t, "scripts:\n  - key: token\n    path: \"$.token\"\n")
	if err == nil {
		t.Fatal("decode = nil, want an error for the old scripts: key")
	}
	if !strings.Contains(err.Error(), "scripts") {
		t.Errorf("decode = %q, want it to name the scripts field", err)
	}
}
