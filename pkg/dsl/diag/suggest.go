package diag

import "strings"

// Nearest returns the candidate closest to word by edit distance, and whether
// one was close enough to be worth printing.
//
// "Did you mean" is only useful when it is nearly always right, so the
// threshold is deliberately tight: at most two edits, and never more than a
// third of the word's length rounded down -- so a 3-character word tolerates
// one edit and nothing tolerates a guess that rewrites most of it. A pure
// difference in case ("Status" for "status") always wins, because that is a
// certainty rather than a guess.
//
// Candidates are compared in the order given and the first of an equal-distance
// tie wins, so a caller that passes the names in scope in a meaningful order --
// a step's own bindings before the scenario's -- gets that order respected.
func Nearest(word string, candidates []string) (string, bool) {
	if word == "" {
		return "", false
	}
	for _, c := range candidates {
		if c != word && strings.EqualFold(c, word) {
			return c, true
		}
	}

	limit := len(word) / 3
	if limit > 2 {
		limit = 2
	}
	if limit < 1 {
		return "", false
	}

	best, bestDist := "", limit+1
	for _, c := range candidates {
		if c == word {
			continue
		}
		if d := distance(word, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best, best != ""
}

// DidYouMean attaches the one phrasing the whole front end uses for a near
// miss -- the hint and the mechanical fix together -- and reports whether a
// candidate was close enough. A caller that gets false should say what is in
// scope instead of guessing:
//
//	ref := b.Error(span, UnknownField, "unknown field %q", name)
//	if !ref.DidYouMean(name, scope) {
//		ref.Hintf("in scope here: %s", strings.Join(scope, ", "))
//	}
func (r *Ref) DidYouMean(word string, candidates []string) bool {
	best, ok := Nearest(word, candidates)
	if !ok || r == nil {
		return false
	}
	r.Hintf("did you mean %q?", best).Suggest(best)
	return true
}

// distance is Levenshtein distance in bytes, with one row of state rather than
// a full matrix. Bytes and not runes: every name the DSL binds is ASCII, and a
// multi-byte identifier only ever produces a slightly worse ranking, never a
// wrong answer to the question "is this close enough".
func distance(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
