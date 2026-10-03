package print

import (
	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/token"
)

// stepItems is a step's action and statements in the order they should print.
//
// ast.StepDecl.items() -- which ast.Children returns -- orders them by byte
// offset, found rather than remembered, so the ordering cannot disagree with
// the spans. That is right for a parsed tree and wrong for an edited one: a
// synthetic node has a zero span, so a statement a UI *appended* to a step
// would sort to offset 0 and print above the action block, where it is a
// diagnostic (`action-not-first`) rather than the line the user asked for.
//
// So the printer orders the step itself, and gives a synthetic item the
// position of the item before it in the slice. An appended statement prints
// last, a synthetic action prints first because the action heads the list, and
// for a tree with no edits in it the result is exactly ast.Children's.
func stepItems(s *ast.StepDecl) []ast.Node {
	list := make([]ast.Node, 0, len(s.Body)+1)
	if !isNil(s.Action) {
		list = append(list, s.Action)
	}
	for _, st := range s.Body {
		list = append(list, st)
	}

	// key[i] is the item's own offset, or the running maximum for a synthetic
	// one, which is what keeps it where the slice put it.
	keys := make([]int, len(list))
	run := 0
	for i, it := range list {
		if off := it.Span().Offset; !it.Span().IsZero() || off > 0 {
			run = off
		}
		keys[i] = run
	}

	// Insertion sort, stable, so equal keys keep slice order -- which is the
	// whole point for the synthetic ones.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return list
}

// blockIndent is the indentation of the first intact item in a list, read off
// the whitespace after the last line break in its leading trivia.
//
// Preserving mode needs it to place a node it has to re-render: a new field in
// a block indented with tabs gets a tab, not two spaces, because the file it is
// being written into is the authority and not this package's constants.
func blockIndent(items []ast.Node) (string, bool) {
	for _, it := range items {
		if isNil(it) || !intact(it) {
			continue
		}
		ts := it.Tokens(nil)
		if len(ts) == 0 {
			continue
		}
		if ind, ok := indentOf(ts[0]); ok {
			return ind, true
		}
	}
	return "", false
}

// indentOf is the whitespace between the last line break in t's leading trivia
// and t itself. A token that does not start a line has none, and says so.
func indentOf(t token.Token) (string, bool) {
	start := -1
	for i, tr := range t.Leading {
		if tr.Kind == token.Newline {
			start = i
		}
	}
	if start < 0 {
		return "", false
	}
	ind := ""
	for _, tr := range t.Leading[start+1:] {
		if tr.Kind != token.Whitespace {
			return "", false // a comment sits between the line break and the token
		}
		ind += tr.Text
	}
	return ind, true
}
