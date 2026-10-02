package models

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeStep decodes one step the way the loader does, strict fields and all.
func decodeStep(t *testing.T, body string) (Step, error) {
	t.Helper()

	var step Step
	dec := yaml.NewDecoder(strings.NewReader(body))
	dec.KnownFields(true)
	err := dec.Decode(&step)
	return step, err
}

func TestTheExecBlockDecodes(t *testing.T) {
	step, err := decodeStep(t, `
name: "seed"
type: exec
exec:
  command: "psql"
  args: ["-f", "seed.sql"]
  cwd: "db"
  env:
    PGPASSWORD: "{{pw}}"
  stdin: "\\q\n"
`)
	if err != nil {
		t.Fatalf("decode = %v, want nil", err)
	}
	want := Exec{
		Command: "psql",
		Args:    []string{"-f", "seed.sql"},
		Cwd:     "db",
		Env:     map[string]string{"PGPASSWORD": "{{pw}}"},
		Stdin:   "\\q\n",
	}
	if !reflect.DeepEqual(step.Exec, want) {
		t.Errorf("exec = %#v, want %#v", step.Exec, want)
	}
}

func TestTheExpectBlockDecodes(t *testing.T) {
	step, err := decodeStep(t, `
name: "seed"
type: exec
expect:
  exit_code: 3
  stdout:
    - value: "COPY 42"
    - operator: matches
      value: "^COPY [0-9]+$"
  stderr:
    - operator: empty
`)
	if err != nil {
		t.Fatalf("decode = %v, want nil", err)
	}
	want := Expect{
		ExitCode: 3,
		Stdout:   []TextCheck{{Value: "COPY 42"}, {Operator: "matches", Value: "^COPY [0-9]+$"}},
		Stderr:   []TextCheck{{Operator: "empty"}},
	}
	if !reflect.DeepEqual(step.Expect, want) {
		t.Errorf("expect = %#v, want %#v", step.Expect, want)
	}
}

// A step that says nothing about the exit code expects the command to have
// succeeded. The zero value is the assertion, so there is nothing to default.
func TestAnAbsentExpectMeansExitCodeZero(t *testing.T) {
	step, err := decodeStep(t, "name: \"ls\"\ntype: exec\nexec:\n  command: \"ls\"\n")
	if err != nil {
		t.Fatalf("decode = %v, want nil", err)
	}
	if step.Expect.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", step.Expect.ExitCode)
	}
	if step.Expect.Stdout != nil || step.Expect.Stderr != nil {
		t.Errorf("expect = %#v, want no stream checks", step.Expect)
	}
}

// Strict decoding has to reach inside the new blocks too: a typo in `exec:` or
// `expect:` must be a load error naming its line, not a key that is silently
// dropped and a step that runs something other than what was written.
func TestATypoInsideTheNewBlocksIsALoadError(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantIn string
	}{
		{"exec", "type: exec\nexec:\n  commnad: \"ls\"\n", "commnad"},
		{"expect", "type: exec\nexpect:\n  exitcode: 1\n", "exitcode"},
		{"stream", "type: exec\nexpect:\n  stdout:\n    - contians: \"x\"\n", "contians"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeStep(t, tc.body)
			if err == nil {
				t.Fatalf("decode = nil, want an error about %s", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %v, want it to name %q", err, tc.wantIn)
			}
		})
	}
}

func TestEnvKeysAreSorted(t *testing.T) {
	e := Exec{Env: map[string]string{"B": "2", "a": "1", "C": "3"}}
	want := []string{"B", "C", "a"}
	if got := e.EnvKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("EnvKeys() = %v, want %v", got, want)
	}
	if got := (Exec{}).EnvKeys(); got != nil {
		t.Errorf("EnvKeys() on an empty env = %v, want nil", got)
	}
}
