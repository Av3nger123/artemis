package shared

import (
	"artemis/pkg/shared/models"
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
    scripts:
      - key: "token"
        path: "$.token"
`

func parse(t *testing.T, yaml string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ParseYAMLFile(path)
	return err
}

func TestParseYAMLFileReadsAFullScenario(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := ParseYAMLFile(path)
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
	if len(step.Scripts) != 1 || step.Scripts[0].Key != "token" {
		t.Errorf("step.Scripts = %+v, want one capture of token", step.Scripts)
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
		"step":       {"    type: api\n", "    type: api\n    timeout: 5\n", "timeout"},
		"request":    {`method: "POST"`, `verb: "POST"`, "verb"},
		"response":   {"status_code: 200", "status: 200", "status"},
		"body check": {`path: "$.token"`, `pth: "$.token"`, "pth"},
		"script":     {`key: "token"`, `kye: "token"`, "kye"},
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

// chdir moves to dir for the length of the test. testing.T.Chdir needs a newer
// go directive than this module declares.
func chdir(t *testing.T, dir string) {
	t.Helper()
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(was); err != nil {
			t.Fatal(err)
		}
	})
}

func TestParseYAMLFileMissingFileErrorsAsNotExist(t *testing.T) {
	_, err := ParseYAMLFile(filepath.Join(t.TempDir(), "nope.yaml"))
	if !os.IsNotExist(err) {
		t.Fatalf("ParseYAMLFile() = %v, want a not-exist error", err)
	}
}

// A generated file has to pass the same validation a hand-written one does:
// strict decoding and a known step type on every step.
func TestConvertJsonToYamlProducesAParseableScenario(t *testing.T) {
	// ConvertJsonToYaml writes the slugified name into the working directory
	// (its filePath argument is unused), so the test has to move there.
	chdir(t, t.TempDir())

	collection := models.PostmanCollection{
		Info:      models.Info{Name: "My Collection"},
		Variables: []models.PostmanVariable{{Key: "url", Value: "https://api.example.com"}},
		Items: []models.Item{{
			Name: "login",
			Request: models.PMRequest{
				Method: "POST",
				Url:    models.RawField{Raw: "https://api.example.com/token"},
				Body:   models.RawField{Raw: `{"user":"u"}`},
			},
		}},
	}

	if err := ConvertJsonToYaml(collection, ""); err != nil {
		t.Fatalf("ConvertJsonToYaml() = %v, want nil", err)
	}

	config, err := ParseYAMLFile("my-collection.yaml")
	if err != nil {
		t.Fatalf("ParseYAMLFile() on the generated file = %v, want nil", err)
	}
	if len(config.Steps) != 1 || config.Steps[0].Type != "api" {
		t.Errorf("generated steps = %+v, want one api step", config.Steps)
	}
}

// SubstituteEnvVars resolves {{env.NAME}} in a scenario's variables before any
// step runs, so a token lives in the environment and not in the file.
func TestSubstituteEnvVars(t *testing.T) {
	t.Setenv("ARTEMIS_TEST_HOST", "api.example.com")
	t.Setenv("ARTEMIS_TEST_TOKEN", "sekret")
	t.Setenv("ARTEMIS_TEST_EMPTY", "")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no placeholder", "https://api.example.com", "https://api.example.com"},
		{"empty", "", ""},
		{"whole value", "{{env.ARTEMIS_TEST_TOKEN}}", "sekret"},
		{"inside text", "https://{{env.ARTEMIS_TEST_HOST}}/v1", "https://api.example.com/v1"},
		{"two references", "{{env.ARTEMIS_TEST_HOST}}:{{env.ARTEMIS_TEST_TOKEN}}", "api.example.com:sekret"},
		{"repeated reference", "{{env.ARTEMIS_TEST_TOKEN}}{{env.ARTEMIS_TEST_TOKEN}}", "sekretsekret"},
		{"unset name is empty", "a{{env.ARTEMIS_TEST_UNSET}}b", "ab"},
		{"set but empty", "a{{env.ARTEMIS_TEST_EMPTY}}b", "ab"},
		{"a scenario variable is left for the templater", "{{token}}", "{{token}}"},
		{"env and scenario placeholders together", "{{token}} {{env.ARTEMIS_TEST_TOKEN}}", "{{token}} sekret"},
		{"scenario placeholder first", "{{other}}{{env.ARTEMIS_TEST_TOKEN}}", "{{other}}sekret"},
		{"a stray close before a reference", "}}{{env.ARTEMIS_TEST_TOKEN}}", "}}sekret"},
		{"unclosed reference is copied through", "a{{env.ARTEMIS_TEST_TOKEN", "a{{env.ARTEMIS_TEST_TOKEN"},
		{"second reference unclosed", "{{env.ARTEMIS_TEST_TOKEN}}/{{env.X", "sekret/{{env.X"},
		{"lone brace", "a{b", "a{b"},
		{"prefix only", "{{env.", "{{env."},
		{"empty name", "{{env.}}", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SubstituteEnvVars(c.in)
			if got != c.want {
				t.Errorf("SubstituteEnvVars(%q) = %#v, want %q", c.in, got, c.want)
			}
		})
	}
}

// A value that references itself used to spin forever, and a closing delimiter
// before an opening one used to panic on a reversed slice. Neither is reachable
// now that the scan only moves forward, and this is the test that says so.
func TestSubstituteEnvVarsTerminatesAndNeverPanics(t *testing.T) {
	t.Setenv("ARTEMIS_TEST_SELF", "{{env.ARTEMIS_TEST_SELF}}")

	full := "}}a{{env.ARTEMIS_TEST_SELF}}b{{env.}}c{{other}}d{{env.X"
	for i := 0; i <= len(full); i++ {
		prefix := full[:i]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("SubstituteEnvVars(%q) panicked: %v", prefix, r)
				}
			}()
			_ = SubstituteEnvVars(prefix)
		}()
	}
}

func TestExtractValue(t *testing.T) {
	data := map[string]interface{}{
		"token": "abc",
		"id":    float64(7),
		"ok":    true,
		"user":  map[string]interface{}{"name": "ada", "roles": []interface{}{"admin", "dev"}},
		"items": []interface{}{
			map[string]interface{}{"sku": "x1"},
			map[string]interface{}{"sku": "x2"},
		},
	}

	cases := []struct {
		name string
		path string
		want interface{}
	}{
		{"top-level string", "$.token", "abc"},
		{"number stays a float64", "$.id", float64(7)},
		{"bool", "$.ok", true},
		{"nested", "$.user.name", "ada"},
		{"array index", "$.items[0].sku", "x1"},
		{"second element", "$.items[1].sku", "x2"},
		{"inside a nested array", "$.user.roles[1]", "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExtractValue(data, models.Script{Key: "k", Path: c.path})
			if err != nil {
				t.Fatalf("ExtractValue(%q) = %v, want nil", c.path, err)
			}
			if got != c.want {
				t.Errorf("ExtractValue(%q) = %#v, want %#v", c.path, got, c.want)
			}
		})
	}
}

func TestExtractValueUnresolvablePathIsAnError(t *testing.T) {
	data := map[string]interface{}{"token": "abc", "items": []interface{}{}}

	for _, path := range []string{"$.nope", "$.token.deeper", "$.items[0]", "", "not a path"} {
		t.Run(path, func(t *testing.T) {
			got, err := ExtractValue(data, models.Script{Key: "k", Path: path})
			if err == nil {
				t.Errorf("ExtractValue(%q) = %#v, want an error", path, got)
			}
		})
	}
}
