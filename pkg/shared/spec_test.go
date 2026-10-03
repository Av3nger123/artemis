package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/dsl/token"
	"artemis/pkg/report"
	"artemis/pkg/shared/migrate"
)

// specPath is SPEC.md relative to this package's directory, and goldenPath is
// the whole JSON report document SPEC.md's schema section has to account for.
const (
	specPath   = "../../SPEC.md"
	goldenPath = "../cli/testdata/art/report_json.golden"
)

// wantSpecScenarios is the number of whole-scenario examples SPEC.md is expected
// to carry. The tests assert they found at least this many, so an edit that
// renames the fences or deletes the examples fails here instead of leaving a
// test that passes on nothing.
const wantSpecScenarios = 4

// SPEC.md specifies the `.art` DSL, which nothing parses yet: subsystem A is
// what will read it. So its examples cannot be run through a loader the way the
// README's are, and these tests check the two things that are checkable and are
// exactly the two that rot:
//
//   - the examples, for the structural mistakes a reader would hit (unbalanced
//     braces) and for having drifted back to the YAML format the DSL replaces;
//   - the spec's own lists, against the code that already defines them -- the
//     type names token.TypeNames admits, the YAML operators pkg/shared/migrate
//     still has to translate, and the report's schema version and its keys.
//
// When the front end lands, the lint below should be replaced by parsing every
// example, which is strictly better and is what readme_test.go already does for
// the YAML.

// TestSpecArtExamplesAreStructurallySound holds every fenced art block in
// SPEC.md to balanced braces, and counts the whole scenarios among them.
//
// Brace counting is a weak check and is meant to be: it catches the example that
// lost a closing brace in an edit, which is the mistake a reader cannot work
// around, without pretending to be a parser.
func TestSpecArtExamplesAreStructurallySound(t *testing.T) {
	scenarios := 0
	for _, b := range docBlocks(t, specPath, "art") {
		if strings.HasPrefix(strings.TrimSpace(b.body), "scenario ") {
			scenarios++
		}
		if depth, err := braceDepth(b.body); err != nil {
			t.Errorf("SPEC.md line %d: %v\n%s", b.line, err, b.body)
		} else if depth != 0 {
			t.Errorf("SPEC.md line %d: %d brace(s) are never closed\n%s", b.line, depth, b.body)
		}
	}

	if scenarios < wantSpecScenarios {
		t.Errorf("found %d whole-scenario examples in SPEC.md, want at least %d -- did the examples move or lose their ```art fences?",
			scenarios, wantSpecScenarios)
	}
}

// TestSpecExamplesHaveNoYAMLisms holds SPEC.md's examples to the format SPEC.md
// specifies.
//
// The spec was rewritten from the YAML surface to the DSL, so the live risk is
// an example that drifts back: a `{{name}}` placeholder where `"${name}"`
// belongs, or a `type:` key on a step, which the DSL does not have at all. Both
// are the sort of thing an agent copies without questioning.
func TestSpecExamplesHaveNoYAMLisms(t *testing.T) {
	yamlisms := []struct {
		substring string
		why       string
	}{
		{"{{", `a YAML-style placeholder; the DSL writes "${name}"`},
		{"type:", `a YAML-style step type; the DSL infers the type from the action block`},
		{"status_code:", `a YAML-style key; the DSL writes expect status == 200`},
		{"operator:", `a YAML-style key; the DSL writes the comparison as an expression`},
	}
	for _, b := range docBlocks(t, specPath, "art") {
		for _, y := range yamlisms {
			if strings.Contains(b.body, y.substring) {
				t.Errorf("SPEC.md line %d: example contains %q -- %s\n%s", b.line, y.substring, y.why, b.body)
			}
		}
	}
}

// TestSpecTypeNamesMatchTheCode holds the six type names SPEC.md documents for
// `is` to the list the checker actually accepts. A seventh name in the code and
// not in the spec is an undocumented feature; a name in the spec and not in the
// code is a documented one that does not work.
//
// The list is token.TypeNames, which is what pkg/dsl/check validates the
// right-hand side of `is` against. It was pkg/shared/assert's TypeNames until
// ART-40 deleted that package; the two were identical, and migrate.TypeNames
// is held to the same list below so a YAML `type: number` keeps meaning what
// `is number` means.
func TestSpecTypeNamesMatchTheCode(t *testing.T) {
	spec := docText(t, specPath)
	for _, name := range token.TypeNames {
		if !strings.Contains(spec, "`"+name+"`") {
			t.Errorf("SPEC.md does not document the type name %q, which token.TypeNames accepts", name)
		}
	}

	// The spec's own list, as it is written for `is`. Kept here rather than
	// scraped out of the prose: the point is that the two agree, and a scraper
	// that silently matched nothing would pass whatever the spec said.
	documented := []string{"string", "number", "boolean", "object", "array", "null"}
	if len(documented) != len(token.TypeNames) {
		t.Errorf("SPEC.md documents %d type names, token.TypeNames has %d: %v vs %v",
			len(documented), len(token.TypeNames), documented, token.TypeNames)
	}

	// The YAML `type:` a migration translates has to name the same six, or a
	// scenario that loaded would migrate into one the checker rejects.
	if !reflect.DeepEqual(migrate.TypeNames, token.TypeNames) {
		t.Errorf("migrate.TypeNames = %v, token.TypeNames = %v; they have to be the same list", migrate.TypeNames, token.TypeNames)
	}
}

// TestSpecCoversEveryOperator holds SPEC.md's operator table to the operators a
// YAML scenario could name, which pkg/shared/migrate is now the only thing that
// knows about.
//
// The mapping is the table from the design document: the DSL replaces the
// `operator:` name with a token or a predicate. Every operator migration can
// translate, including the text-only ones a terminal step used to name, has to
// be reachable from SPEC.md -- `empty` by the pattern the spec says to write
// instead, since the DSL has no `empty` operator. An operator with no row here
// is one a migrated file would spell in a way the spec never defined.
func TestSpecCoversEveryOperator(t *testing.T) {
	spec := docText(t, specPath)

	// Each entry is a YAML operator and what SPEC.md must contain to count as
	// having specified it.
	dsl := map[string]string{
		migrate.OpEquals:   "`==`",
		migrate.OpContains: "`contains`",
		migrate.OpMatches:  "`matches`",
		migrate.OpExists:   "`exists`",
		migrate.OpType:     "`is <type>`",
		migrate.OpGt:       "`>`",
		migrate.OpGte:      "`>=`",
		migrate.OpLt:       "`<`",
		migrate.OpLte:      "`<=`",
		migrate.OpEmpty:    `matches /^\s*$/`,
	}

	for _, ops := range [][]string{migrate.Operators, migrate.TextOperators} {
		for _, op := range ops {
			want, known := dsl[op]
			if !known {
				t.Errorf("migrate has an operator %q that SPEC.md's mapping does not know about -- add it to the spec and to this table", op)
				continue
			}
			if !strings.Contains(spec, want) {
				t.Errorf("SPEC.md does not specify %q (the operator %q lowers to): expected to find %s in it", want, op, want)
			}
		}
	}
}

// TestSpecDocumentsTheReportSchema holds SPEC.md's schema section to the
// document artemis actually writes: the version it stamps, and every key in a
// real report.
//
// The golden is a whole document from a real run, so a key added to the report
// and not to the spec fails here. It is read as JSON rather than as text so a
// key that only appears in a comment does not count.
func TestSpecDocumentsTheReportSchema(t *testing.T) {
	spec := docText(t, specPath)

	version := fmt.Sprintf(`"schema_version": %d`, report.SchemaVersion)
	if !strings.Contains(spec, version) {
		t.Errorf("SPEC.md does not show %s; report.SchemaVersion is %d", version, report.SchemaVersion)
	}

	doc := goldenReport(t)

	for _, key := range jsonKeys(doc, nil) {
		if !strings.Contains(spec, "`"+key+"`") {
			t.Errorf("SPEC.md does not document the report key %q, which %s contains", key, goldenPath)
		}
	}
}

// goldenReport is the JSON document out of the golden file.
//
// The golden holds a whole run -- the console report, then the JSON document on
// stdout -- so the document is read from the first line that is a bare brace,
// with a decoder, which stops at the end of the value and ignores whatever
// follows it.
func goldenReport(t *testing.T) any {
	t.Helper()

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading %s: %v", goldenPath, err)
	}
	start := strings.Index(string(raw), "\n{\n")
	if start < 0 {
		t.Fatalf("%s holds no JSON document -- did the golden's shape change?", goldenPath)
	}

	var doc any
	if err := json.NewDecoder(strings.NewReader(string(raw)[start+1:])).Decode(&doc); err != nil {
		t.Fatalf("the JSON document in %s does not decode: %v", goldenPath, err)
	}
	return doc
}

// jsonKeys returns every distinct object key in a decoded JSON document, sorted
// by nothing in particular -- the caller only asks whether each is documented.
func jsonKeys(v any, found []string) []string {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			if !contains(found, key) {
				found = append(found, key)
			}
			found = jsonKeys(child, found)
		}
	case []any:
		for _, child := range t {
			found = jsonKeys(child, found)
		}
	}
	return found
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// braceDepth returns how many braces of body are still open at its end, and an
// error for a closing brace with nothing to close.
func braceDepth(body string) (int, error) {
	depth := 0
	for i, r := range body {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return 0, fmt.Errorf("a closing brace at offset %d has nothing to close", i)
			}
		}
	}
	return depth, nil
}
