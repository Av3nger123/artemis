package ast

import (
	"strings"

	"artemis/pkg/dsl/token"
)

// Source is the source a node was parsed from, exactly: its tokens' text with
// their trivia, concatenated.
//
//	Source(Parse(file, src)) == src
//
// for every input, which is the one assertion that proves the tree is
// genuinely concrete. It is the structural half of the lossless round trip;
// pkg/dsl/print owns the other half, where a node that was *edited* has to be
// re-rendered and the rest left alone.
//
// This has no modes and no options on purpose. A function that concatenates
// cannot be wrong in an interesting way, so when the round-trip test fails the
// bug is always in the tree, which is where it is worth finding.
func Source(n Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for _, t := range n.Tokens(nil) {
		token.WriteTrivia(&b, t.Leading)
		b.WriteString(t.Text)
		token.WriteTrivia(&b, t.Trailing)
	}
	return b.String()
}
