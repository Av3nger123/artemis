package expand

import (
	"strconv"
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

// expanded is one scenario file through expansion and the checker, as
// pkg/dsl/front runs them.
type expanded struct {
	out   string // the canonical text of the expanded file
	diags string // every diagnostic, one per line: file:line: code: message
	res   *Result
	bag   *diag.Bag
}

// expandFlows expands main, a scenario file named main.art, with files beside
// it.
func expandFlows(t *testing.T, main string, files map[string]string) expanded {
	t.Helper()
	tree, pb := parser.Parse("main.art", main)
	if pb.HasErrors() {
		t.Fatalf("main.art does not parse: %v", pb.All())
	}
	res, bag := Expand(tree, MapLoader(files))
	_, cb := check.CheckChained(res.File, res.Chain)
	for _, d := range cb.All() {
		d.UsedFrom = res.Chain(d.Span)
		bag.Add(d)
	}
	var msgs []string
	for _, d := range bag.All() {
		msgs = append(msgs, d.Span.File+":"+strconv.Itoa(d.Span.Line)+": "+string(d.Code)+": "+d.Message)
	}
	return expanded{out: print.Canonical(res.File), diags: strings.Join(msgs, "\n"), res: res, bag: bag}
}

// only is the diagnostics of bag with code.
func only(bag *diag.Bag, code diag.Code) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range bag.All() {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}
