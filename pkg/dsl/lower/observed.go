package lower

import (
	"sort"
	"strconv"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/taint"
	"artemis/pkg/dsl/token"
	"artemis/pkg/shared/models"
)

// This file is the observation half of ART-54's marks: what a trace of a step
// must withhold from what the step *saw*, as opposed to from what it sent.
//
// The request half is in action.go, and it is driven by which bindings an
// expression reads. This half is driven by the opposite question -- where a
// secret binding's value came *from* -- because a `secret capture` names a
// location in the response, and the response is where the credential was before
// it was ever a binding.

// observedMarks records what a trace must withhold from the step's roots,
// given the captures that are secret.
//
// A capture written as a path into a root is addressable, so only that path is
// withheld and the rest of the response stays readable: `capture token =
// body.data.access_token` withholds `/data/access_token` of `body` and nothing
// else. A capture that reads a root any other way -- `match(raw, /tok=(.)/)`,
// an index by a variable -- names no location a reader could match, so the whole
// root goes.
func observedMarks(secret []*Capture) (roots map[string]bool, paths map[string][]string) {
	for _, c := range secret {
		root, pointer, ok := rootPath(c.Value)
		if root == "" {
			// The expression reads no root at all: a capture off a var. There is
			// nothing in the observation to withhold.
			continue
		}
		if !ok {
			if roots == nil {
				roots = map[string]bool{}
			}
			roots[root] = true
			continue
		}
		if pointer == "" {
			// The capture is the whole root: `capture all = body`.
			if roots == nil {
				roots = map[string]bool{}
			}
			roots[root] = true
			continue
		}
		if paths == nil {
			paths = map[string][]string{}
		}
		paths[root] = append(paths[root], pointer)
	}
	// A root withheld whole makes its own paths redundant, and keeping both
	// would let a reader think the rest of that root survived.
	for name := range roots {
		delete(paths, name)
	}
	return roots, paths
}

// rootPath splits an expression into the root it reads and an RFC 6901 pointer
// within it.
//
// ok is false when the expression reads a root through something this cannot
// address: a call, an index by anything but a literal, an operator. root is
// empty when the expression reads no root at all, which the caller treats as
// "nothing in the observation to withhold" rather than as a failure.
//
// Only Member and Index are walked, because those are the two forms that
// address a location. `body.data[0].id` is addressable; `match(raw, /x/)` is
// not, and neither is `body.items[i]`.
func rootPath(e ast.Expr) (root, pointer string, ok bool) {
	var segments []string
	for {
		switch v := e.(type) {
		case *ast.Ident:
			// The bottom of the chain. Reversed, because the walk collected the
			// segments from the outside in.
			for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
				segments[i], segments[j] = segments[j], segments[i]
			}
			return v.Name(), joinPointer(segments), true

		case *ast.Member:
			segments = append(segments, escapePointer(v.Name.Value))
			e = v.X

		case *ast.Index:
			lit, isLit := v.Index.(*ast.Literal)
			if !isLit {
				// An index by a variable or a call. The location depends on a
				// run-time value, so there is no pointer to write down.
				return rootOf(v.X), "", false
			}
			seg := lit.Tok.Value
			if lit.Tok.Kind == token.Number {
				// A JSON pointer indexes an array by its decimal position, and
				// a number literal arrives here as its source text.
				if f, err := strconv.ParseFloat(seg, 64); err == nil {
					seg = strconv.Itoa(int(f))
				}
			}
			segments = append(segments, escapePointer(seg))
			e = v.X

		case *ast.Paren:
			e = v.X

		default:
			// A call, an operator, a literal: not addressable. Name the root it
			// reads if there is one, so the caller can withhold it whole.
			return rootOf(e), "", false
		}
	}
}

// rootOf is the first identifier an expression reads, which is the root it
// withholds, or empty when it reads none.
//
// It is deliberately a different question from rootPath's: this one is asked
// after addressing has already failed, and all that is left to decide is which
// root to withhold.
//
// A Call's callee is skipped, for the reason taint.Secret skips it: a callee is
// a function name, so `match(raw, /x/)` reads `raw` and not a root called
// `match`. ast.Walk visits Callee first, which is exactly the wrong answer.
func rootOf(e ast.Expr) string {
	switch v := e.(type) {
	case nil:
		return ""
	case *ast.Ident:
		return v.Name()
	case *ast.Call:
		for _, a := range v.Args {
			if got := rootOf(a.Value); got != "" {
				return got
			}
		}
		return ""
	case *ast.Member:
		return rootOf(v.X)
	case *ast.Index:
		if got := rootOf(v.X); got != "" {
			return got
		}
		return rootOf(v.Index)
	case *ast.Paren:
		return rootOf(v.X)
	case *ast.Unary:
		return rootOf(v.X)
	case *ast.Binary:
		if got := rootOf(v.X); got != "" {
			return got
		}
		return rootOf(v.Y)
	case *ast.Exists:
		return rootOf(v.X)
	case *ast.IsType:
		return rootOf(v.X)
	case *ast.Interp:
		for _, seg := range v.Segments {
			if got := rootOf(seg.Expr); got != "" {
				return got
			}
		}
		return ""
	case *ast.Object:
		for _, en := range v.Entries {
			if got := rootOf(en.Value); got != "" {
				return got
			}
		}
		return ""
	case *ast.Array:
		for _, el := range v.Elems {
			if got := rootOf(el.Value); got != "" {
				return got
			}
		}
		return ""
	}
	return ""
}

// joinPointer builds the pointer from already-escaped segments. No segments is
// the empty pointer, which addresses the whole root.
func joinPointer(segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	return "/" + strings.Join(segments, "/")
}

// secretCaptures is the step's captures that are secret, by the same two rules
// the scenario walk uses: the author wrote `secret capture`, or the captured
// expression reads a secret binding.
func secretCaptures(st *Step, secrets map[string]bool) []*Capture {
	var out []*Capture
	for _, c := range st.Captures {
		if c.Declared || taint.Secret(c.Value, secrets) {
			out = append(out, c)
		}
	}
	return out
}

// observed fills the observation half of a step's marks. It is called once, at
// lowering time, because every input to it is a compile-time fact.
func (s *Step) observed() models.Secrets {
	var out models.Secrets
	out.ObservedRoots, out.ObservedPaths = observedMarks(secretCaptures(s, s.secretNames))
	return out
}

// SecretNames are the bindings in scope for this step that the scenario declared
// secret, or that a secret binding's value flowed into.
//
// It exists for the trace, and for one reason the static marks cannot cover. A
// mark says *where this scenario read a value from*. It cannot say everywhere
// that value might turn up, because a response is run-time data: a service that
// echoes a token back in a later response puts the credential somewhere no
// capture ever named. So the runner resolves these names against the scope and
// the trace scrubs the resulting strings wherever they appear.
//
// The two layers do different jobs and both are needed. The marks are precise --
// they withhold one body field and leave the rest readable. This is a net, and
// it catches what precision cannot know about.
func (s *Step) SecretNames() []string {
	if len(s.secretNames) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.secretNames))
	for name := range s.secretNames {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
