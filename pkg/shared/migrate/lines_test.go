package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// writeScenario puts body in a temp file and returns its path. parser_test.go's
// parse throws the config away; these tests need it.
func writeScenario(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// linedScenario is one scenario with every addressable part on a line this test
// can name. The comment on each line is the line number, counting the `name:`
// line as 1.
const linedScenario = `name: "lines"
type: functional
variables: []
steps:
  - name: "fetch"
    type: api
    request:
      url: "http://example.invalid/things"
      method: "GET"
    response:
      status_code: 200
      body:
        - path: "$.status"
          value: "ok"
        - path: "$.count"
          operator: lte
          value: 2
    capture:
      token: "$.token"
      ref:
        regex: "id=([0-9]+)"
  - name: "build"
    type: exec
    exec:
      command: "true"
    expect:
      exit_code: 0
      stdout:
        - operator: contains
          value: "done"
      stderr:
        - operator: empty
`

func TestParseYAMLFileRecordsTheLineOfEveryPart(t *testing.T) {
	config, err := ParseYAMLFile(writeScenario(t, linedScenario), knownTypes)
	if err != nil {
		t.Fatalf("ParseYAMLFile() = %v, want nil", err)
	}
	if len(config.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(config.Steps))
	}

	api, ex := config.Steps[0], config.Steps[1]
	for _, c := range []struct {
		what string
		got  int
		want int
	}{
		{"api step", api.Line, 5},
		{"status_code", api.Response.StatusCodeLine, 11},
		{"body[0]", api.Response.Body[0].Line, 13},
		{"body[1]", api.Response.Body[1].Line, 15},
		{"capture token", api.Capture["token"].Line, 19},
		{"capture ref", api.Capture["ref"].Line, 20},
		{"exec step", ex.Line, 22},
		{"exit_code", ex.Expect.ExitCodeLine, 27},
		{"stdout[0]", ex.Expect.Stdout[0].Line, 29},
		{"stderr[0]", ex.Expect.Stderr[0].Line, 32},
	} {
		if c.got != c.want {
			t.Errorf("%s line = %d, want %d", c.what, c.got, c.want)
		}
	}
}

// A regex capture's compiled pattern is cached by Capture.UnmarshalYAML. The
// line walk rewrites the map value, so this is the test that the copy does not
// throw the pattern away.
func TestAnnotatingACaptureKeepsItsCompiledPattern(t *testing.T) {
	config, err := ParseYAMLFile(writeScenario(t, linedScenario), knownTypes)
	if err != nil {
		t.Fatalf("ParseYAMLFile() = %v, want nil", err)
	}
	re, err := config.Steps[0].Capture["ref"].Pattern()
	if err != nil {
		t.Fatalf("Pattern() = %v, want nil", err)
	}
	if re == nil || !re.MatchString("id=42") {
		t.Errorf("Pattern() = %v, want the compiled id=([0-9]+)", re)
	}
}

// A step whose checks and captures were never written still gets its own line,
// and the parts it does not have stay at zero rather than borrowing one.
func TestLinesForAStepThatDeclaresNothing(t *testing.T) {
	config, err := ParseYAMLFile(writeScenario(t, `name: "bare"
type: functional
steps:
  - name: "ping"
    type: api
    request:
      url: "http://example.invalid/"
      method: "GET"
`), knownTypes)
	if err != nil {
		t.Fatalf("ParseYAMLFile() = %v, want nil", err)
	}
	step := config.Steps[0]
	if step.Line != 4 {
		t.Errorf("step line = %d, want 4", step.Line)
	}
	if step.Response.StatusCodeLine != 0 {
		t.Errorf("StatusCodeLine = %d, want 0 -- the step never wrote one", step.Response.StatusCodeLine)
	}
	if step.Expect.ExitCodeLine != 0 {
		t.Errorf("ExitCodeLine = %d, want 0", step.Expect.ExitCodeLine)
	}
}

// A scenario cannot set a line itself: the fields are yaml:"-", so strict
// decoding treats `line:` as the typo it is.
func TestAScenarioCannotWriteALine(t *testing.T) {
	_, err := ParseYAMLFile(writeScenario(t, `name: "sneaky"
type: functional
steps:
  - name: "ping"
    type: api
    line: 99
    request:
      url: "http://example.invalid/"
      method: "GET"
`), knownTypes)
	if err == nil {
		t.Fatal("ParseYAMLFile() = nil, want an error: line is not a field a scenario may set")
	}
}

// The second pass must not weaken the first. A typo'd key is still the error it
// was before line numbers existed.
func TestStrictDecodingStillRejectsATypo(t *testing.T) {
	_, err := ParseYAMLFile(writeScenario(t, `name: "typo"
type: functional
steps:
  - name: "ping"
    type: api
    respones:
      status_code: 200
`), knownTypes)
	if err == nil {
		t.Fatal("ParseYAMLFile() = nil, want an error naming the unknown field")
	}
}

// annotateLines is handed node trees no scenario could produce. It must leave
// zeros rather than panic: every caller past the strict decode is committed to
// running the scenario, so a surprise here would take the whole run down.
func TestAnnotateLinesSurvivesAShapeItDoesNotRecognise(t *testing.T) {
	for _, doc := range []string{
		"",
		"steps: not-a-sequence",
		"steps: []",
		"steps:\n  - 3\n",
		"steps:\n  - response: not-a-mapping\n    capture: 7\n",
		"- just\n- a\n- list\n",
	} {
		config, err := ParseYAMLFile(writeScenario(t, `name: "x"
type: functional
steps:
  - name: "ping"
    type: api
`), knownTypes)
		if err != nil {
			t.Fatalf("ParseYAMLFile() = %v, want nil", err)
		}
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(doc), &node); err != nil {
			t.Fatalf("unmarshalling %q: %v", doc, err)
		}
		annotateLines(&config, &node)
	}
}

// A nil node is what a caller gets from an empty document. It is not reachable
// through ParseYAMLFile, and it still must not panic.
func TestAnnotateLinesTakesANilNode(t *testing.T) {
	config, err := ParseYAMLFile(writeScenario(t, `name: "x"
type: functional
steps:
  - name: "ping"
    type: api
`), knownTypes)
	if err != nil {
		t.Fatalf("ParseYAMLFile() = %v, want nil", err)
	}
	annotateLines(&config, nil)
	if config.Steps[0].Line != 4 {
		t.Errorf("a nil node changed a line that was already stamped: %d", config.Steps[0].Line)
	}
}
