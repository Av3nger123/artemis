package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"artemis/pkg/dsl/grammar"
	"artemis/pkg/dsl/token"
)

// The plain form is what gets pasted into a prompt, so the test is that it is
// whole: the productions, the sections around them, and a trailing newline so
// it concatenates with whatever is pasted after it.
func TestGrammarPrintsTheEBNF(t *testing.T) {
	stdout, stderr, err := executeArgs(t, "grammar")
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	if stdout != grammar.Document() {
		t.Error("stdout is not grammar.Document()")
	}
	for _, want := range []string{
		"File          = { Scenario } ;",
		"LEXICAL TOKENS", "PRECEDENCE", "SCOPES", "EXAMPLE",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the printed grammar has no %q", want)
		}
	}
	if !strings.HasSuffix(stdout, "\n") {
		t.Error("the printed grammar does not end in a newline")
	}
}

// --json is the dropdown feed. What matters to a client is that it parses,
// says which version it is, and holds the sets the issue names.
func TestGrammarJSONEmitsTheChoicePoints(t *testing.T) {
	stdout, stderr, err := executeArgs(t, "grammar", "--json")
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	var doc struct {
		SchemaVersion int `json:"schemaVersion"`
		Choices       map[string]struct {
			Doc    string `json:"doc"`
			Values []struct {
				Value      string `json:"value"`
				TakesValue bool   `json:"takesValue"`
				ValueKind  string `json:"valueKind"`
			} `json:"values"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if doc.SchemaVersion != grammar.SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", doc.SchemaVersion, grammar.SchemaVersion)
	}

	// The five the issue names by hand, plus the step scopes a path picker
	// needs. Each is checked against its own token table, so this stays true
	// as the language grows.
	for choice, want := range map[string][]string{
		"method":        token.Methods,
		"browserAction": token.BrowserActions,
		"comparison":    token.Comparisons,
		"typeName":      token.TypeNames,
		"retryField":    token.RetryFields,
	} {
		c, ok := doc.Choices[choice]
		if !ok {
			t.Errorf("the document has no %q choice point", choice)
			continue
		}
		if len(c.Values) != len(want) {
			t.Errorf("%s has %d values, token has %d", choice, len(c.Values), len(want))
			continue
		}
		for i, v := range c.Values {
			if v.Value != want[i] {
				t.Errorf("%s value %d = %q, want %q", choice, i, v.Value, want[i])
			}
		}
	}

	// The second fact each set carries, spot-checked through the command so
	// that a client reading this output gets what pkg/dsl/grammar's tests
	// promise it does.
	if kinds := valueKinds(doc.Choices["retryField"].Values); kinds["times"] != "integer" || kinds["delay"] != "duration" {
		t.Errorf("retryField kinds = %v, want times integer and delay duration", kinds)
	}
	for _, v := range doc.Choices["browserAction"].Values {
		if v.Value == "fill" && !v.TakesValue {
			t.Error("fill does not say it takes a value")
		}
		if v.Value == "click" && v.TakesValue {
			t.Error("click says it takes a value")
		}
	}
	if _, ok := doc.Choices["stepType"]; !ok {
		t.Error("the document has no stepType choice point, so a path picker has no input")
	}
	if _, ok := doc.Choices["diagnosticCode"]; !ok {
		t.Error("the document has no diagnosticCode choice point")
	}
}

func valueKinds(values []struct {
	Value      string `json:"value"`
	TakesValue bool   `json:"takesValue"`
	ValueKind  string `json:"valueKind"`
}) map[string]string {
	out := map[string]string{}
	for _, v := range values {
		out[v.Value] = v.ValueKind
	}
	return out
}

// The command takes no file and no arguments: everything it prints is in the
// binary, and a path typed after it is a mistake worth naming.
func TestGrammarTakesNoArguments(t *testing.T) {
	if _, _, err := executeArgs(t, "grammar", "checkout.art"); err == nil {
		t.Error("`artemis grammar checkout.art` was accepted")
	}
}
