package check

import (
	"testing"

	"artemis/pkg/dsl/token"
)

// TestBrowserActsAreExactlyTheTable is one half of the drift gate ART-44 is
// graded on: an action added to token.BrowserActions arrives here with no
// arity and no example, and this says so by name.
func TestBrowserActsAreExactlyTheTable(t *testing.T) {
	acts := BrowserActs()
	if len(acts) != len(token.BrowserActions) {
		t.Fatalf("BrowserActs has %d entries, token.BrowserActions has %d", len(acts), len(token.BrowserActions))
	}
	for i, a := range acts {
		if a.Name != token.BrowserActions[i] {
			t.Errorf("BrowserActs()[%d] = %q, token.BrowserActions[%d] = %q: the order must be the table's",
				i, a.Name, i, token.BrowserActions[i])
		}
		if a.Example == "" {
			t.Errorf("browser action %q has no example; add one to actSignatures in field.go", a.Name)
		}
		if a.TakesValue == oneArg[a.Name] {
			t.Errorf("browser action %q: TakesValue = %v but oneArg = %v; they must disagree",
				a.Name, a.TakesValue, oneArg[a.Name])
		}
	}
}

// TestOneArgNamesAreBrowserActions is the other direction: oneArg keyed by a
// word that is not an action would silently give that word an arity rule
// nothing enforces.
func TestOneArgNamesAreBrowserActions(t *testing.T) {
	for name := range oneArg {
		if !token.IsBrowserAction(name) {
			t.Errorf("oneArg has %q, which is not in token.BrowserActions", name)
		}
	}
	for name := range actSignatures {
		if !token.IsBrowserAction(name) {
			t.Errorf("actSignatures has %q, which is not in token.BrowserActions", name)
		}
	}
}

// TestEveryBlockFieldHasASpec walks the five blocks and fails if a name in a
// token field table has no fieldSpec -- which would mean the checker accepts
// the name, reports nothing about its value, and `artemis grammar --json`
// never offers it.
func TestEveryBlockFieldHasASpec(t *testing.T) {
	for _, block := range BlockNames() {
		spec, ok := blockSpecs[block]
		if !ok {
			t.Fatalf("BlockNames has %q, which blockSpecs does not", block)
		}
		fields, _ := BlockFields(block)
		if len(fields) != len(spec.names) {
			t.Errorf("block %q: %d fields exported, %d names in its table", block, len(fields), len(spec.names))
		}
		if len(spec.fields) != len(spec.names) {
			t.Errorf("block %q: %d specs, %d names; one of them is missing an entry",
				block, len(spec.fields), len(spec.names))
		}
		for _, f := range fields {
			if f.ValueKind == "unknown" {
				t.Errorf("block %q field %q has an unnamed value kind", block, f.Name)
			}
		}
	}
}

// TestBlockFieldOrderIsTheTokenTable pins the three blocks whose order a form
// renders in. It names the tables rather than the words, so the expectation
// moves with the language.
func TestBlockFieldOrderIsTheTokenTable(t *testing.T) {
	cases := map[string][]string{
		"request":       token.RequestFields,
		"run":           token.RunFields,
		"retry":         token.RetryFields,
		"step":          token.StepFields,
		"configBrowser": token.BrowserConfigFields,
	}
	for block, want := range cases {
		fields, ok := BlockFields(block)
		if !ok {
			t.Fatalf("BlockFields(%q) is not a block", block)
		}
		for i, f := range fields {
			if i >= len(want) || f.Name != want[i] {
				t.Errorf("block %q field %d = %q, want %q", block, i, f.Name, want[i])
			}
		}
	}
	if _, ok := BlockFields("nothing"); ok {
		t.Error(`BlockFields("nothing") reported a block`)
	}
}

// TestEveryValueKindIsNamed stops a kind added to field.go from reaching the
// contract as "unknown".
func TestEveryValueKindIsNamed(t *testing.T) {
	for k := kindAny; k <= kindBlock; k++ {
		if _, ok := valueKindNames[k]; !ok {
			t.Errorf("valueKind(%d) has no name in valueKindNames", k)
		}
	}
}

// TestReservedWordsCarryTheirPurpose repeats the reserved-word gate through
// the accessor the contract uses, so an empty purpose cannot reach a client.
func TestReservedWordsCarryTheirPurpose(t *testing.T) {
	words := ReservedWords()
	if len(words) != len(token.Reserved) {
		t.Fatalf("ReservedWords has %d entries, token.Reserved has %d", len(words), len(token.Reserved))
	}
	for i, w := range words {
		if w.Word != token.Reserved[i] {
			t.Errorf("ReservedWords()[%d] = %q, want %q", i, w.Word, token.Reserved[i])
		}
		if w.Purpose == "" {
			t.Errorf("reserved word %q has no purpose", w.Word)
		}
	}
}

// TestStepTypesAndClassesStringify guards the two label sets the tree encoding
// and the choice points share: a type or a class whose String is empty would
// ship as "".
func TestStepTypesAndClassesStringify(t *testing.T) {
	for _, st := range StepTypes() {
		if st == Unknown || st.String() == "" {
			t.Errorf("StepTypes has %d, whose name is %q", st, st.String())
		}
	}
	for _, c := range Classes() {
		if c.String() == "" {
			t.Errorf("Class(%d) has no name", c)
		}
	}
}
