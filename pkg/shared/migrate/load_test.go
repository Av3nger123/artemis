package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// good is a scenario with one of everything the structs understand, so a test
// that splices a typo into it is testing strictness and nothing else.
const good = `name: "orders"
type: functional
variables:
  - name: "url"
    value: "https://api.example.com"
steps:
  - name: "login"
    type: api
    retry:
      times: 3
      delay: "10ms"
    request:
      url: "{{url}}/token"
      method: "POST"
      headers:
        Content-Type: "application/json"
      body: '{"user":"u"}'
    response:
      status_code: 200
      body:
        - path: "$.token"
          operator: exists
        - path: "$.count"
          operator: gt
          value: 0
          type: number
    capture:
      token: "$.token"
      requestId: {regex: "req-([0-9a-f]+)"}
`

// knownTypes stands in for what the registry hands the loader in a real run.
// This package cannot read the registry -- the executors import it -- so the
// list is written out here, and models' own tests are what pin that Validate
// uses whatever it is given.
var knownTypes = []string{"api", "exec"}

func parse(t *testing.T, yaml string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ParseYAMLFile(path, knownTypes)
	return err
}

func TestParseYAMLFileReadsAFullScenario(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := ParseYAMLFile(path, knownTypes)
	if err != nil {
		t.Fatalf("ParseYAMLFile() = %v, want nil", err)
	}
	if config.Name != "orders" || len(config.Steps) != 1 {
		t.Fatalf("config = %+v, want one step of scenario \"orders\"", config)
	}
	step := config.Steps[0]
	if step.Retry.Times != 3 || step.Retry.Delay != "10ms" {
		t.Errorf("step.Retry = %+v, want {3 10ms}", step.Retry)
	}
	if len(step.Response.Body) != 2 || step.Response.Body[1].Operator != "gt" {
		t.Errorf("step.Response.Body = %+v, want two checks, the second a gt", step.Response.Body)
	}
	if len(step.Capture) != 2 || step.Capture["token"].JSON != "$.token" {
		t.Errorf("step.Capture = %+v, want a json capture of token", step.Capture)
	}
	if step.Capture["requestId"].Regex != "req-([0-9a-f]+)" {
		t.Errorf("step.Capture[requestId] = %+v, want the regex form", step.Capture["requestId"])
	}
}

// Strict decoding at every depth the scenario file has. Without it each of
// these is a key quietly dropped, leaving a scenario that checks less than it
// says and still passes.
func TestParseYAMLFileRejectsUnknownKeys(t *testing.T) {
	// key is the unknown key the substitution introduces: its name is the only
	// thing that tells the user where to look.
	tests := map[string]struct{ from, to, key string }{
		"top level":  {"type: functional", "tpye: functional", "tpye"},
		"step":       {"    type: api\n", "    type: api\n    timeuot: 5s\n", "timeuot"},
		"request":    {`method: "POST"`, `verb: "POST"`, "verb"},
		"response":   {"status_code: 200", "status: 200", "status"},
		"body check": {`path: "$.token"`, `pth: "$.token"`, "pth"},
		"capture":    {`{regex: "req-([0-9a-f]+)"}`, `{rgex: "req-([0-9a-f]+)"}`, "rgex"},
		"retry":      {"times: 3", "tims: 3", "tims"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			yaml := strings.Replace(good, tc.from, tc.to, 1)
			if yaml == good {
				t.Fatalf("test is broken: %q is not in the scenario", tc.from)
			}

			err := parse(t, yaml)
			if err == nil {
				t.Fatalf("ParseYAMLFile() = nil, want an error for the unknown key %q", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error = %q, want it to name %q", err, tc.key)
			}
		})
	}
}

func TestParseYAMLFileRejectsMalformedYAML(t *testing.T) {
	if err := parse(t, "name: [unterminated\n\tsteps: nope\n"); err == nil {
		t.Fatal("ParseYAMLFile() = nil, want an error for malformed YAML")
	}
}

func TestParseYAMLFileRejectsUnknownStepType(t *testing.T) {
	err := parse(t, strings.Replace(good, "type: api", "type: db", 1))
	if err == nil {
		t.Fatal("ParseYAMLFile() = nil, want an error for a step type nothing executes")
	}
	for _, want := range []string{"step 1", "login", `"db"`, "api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestParseYAMLFileMissingFileErrorsAsNotExist(t *testing.T) {
	_, err := ParseYAMLFile(filepath.Join(t.TempDir(), "nope.yaml"), knownTypes)
	if !os.IsNotExist(err) {
		t.Fatalf("ParseYAMLFile() = %v, want a not-exist error", err)
	}
}
