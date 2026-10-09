package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

// run expands main.art from files and returns the canonical text of the
// result and every diagnostic message, one per line.
func run(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	tree, bag := parser.Parse("main.art", files["main.art"])
	if bag.HasErrors() {
		t.Fatalf("main.art does not parse: %v", bag.All())
	}
	res, eb := Expand(tree, MapLoader(files))
	var msgs []string
	for _, d := range eb.All() {
		msgs = append(msgs, d.Span.File+": "+string(d.Code)+": "+d.Message)
	}
	return print.Canonical(res.File), strings.Join(msgs, "\n")
}

func noDiags(t *testing.T, diags string) {
	t.Helper()
	if diags != "" {
		t.Fatalf("unexpected diagnostics:\n%s", diags)
	}
}

// parserParse parses src as main.art.
func parserParse(src string) (*ast.File, *diag.Bag) { return parser.Parse("main.art", src) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }
