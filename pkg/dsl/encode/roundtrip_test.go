package encode

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/print"
)

// TestCanonicalSourceSurvivesTheRoundTrip is the issue's acceptance gate.
//
//	print.Canonical(Decode(Encode(tree))) == print.Canonical(tree)
//
// byte for byte, over the corpus. It is anchored at canonical source because
// that is where the fixed point is: this encoding carries what canonical layout
// carries -- every node, every name, every operator, every literal's source
// text, and every comment -- and `artemis ast --from-json` writes canonical
// layout. Whitespace is not in it, and canonical mode owns whitespace.
//
// Comments are the part that could silently rot, which is why they are in the
// gate rather than in a test of their own: comments.go extracts them by the same
// rule pkg/dsl/print places them by, the two live in different packages, and
// this is what stops them drifting apart.
func TestCanonicalSourceSurvivesTheRoundTrip(t *testing.T) {
	ran := 0
	for path, src := range corpus(t) {
		canon, _, doc, ok := encoded(t, path, src)
		if !ok {
			continue
		}
		ran++
		back, err := Source(doc)
		if err != nil {
			t.Errorf("%s: decoding: %v", path, err)
			continue
		}
		if back != canon {
			t.Errorf("%s: the round trip changed the file\n--- canonical ---\n%s\n--- after the round trip ---\n%s",
				path, canon, back)
		}
	}
	if ran < 20 {
		t.Fatalf("only %d files parsed clean, which is too few to be the corpus", ran)
	}
}

// TestEncodingIsAFixedPoint is the structural half: Encode(Decode(j)) == j,
// byte for byte, spans included.
//
// Spans included is what makes it worth asserting. They line up because Decode
// returns the tree it reparsed from its own canonical print, so both documents'
// spans locate the same source -- and anything that drifted in between, a field
// that decoded to a different shape or a label recomputed differently, shows up
// as a diff here.
func TestEncodingIsAFixedPoint(t *testing.T) {
	for path, src := range corpus(t) {
		_, _, doc, ok := encoded(t, path, src)
		if !ok {
			continue
		}
		tree, err := Decode(doc)
		if err != nil {
			t.Errorf("%s: decoding: %v", path, err)
			continue
		}
		info, _ := check.Check(tree)
		again, err := Encode(tree, info)
		if err != nil {
			t.Errorf("%s: re-encoding: %v", path, err)
			continue
		}
		if string(again) != string(doc) {
			t.Errorf("%s: the encoding is not a fixed point\n%s",
				path, firstDifference(string(doc), string(again)))
		}
	}
}

// TestDecodedSpansLocateTheCanonicalSource is the reason Decode reparses: a
// caller that decodes a tree can check it, lower it, and report a diagnostic
// that points at a line of the file it is about to write.
func TestDecodedSpansLocateTheCanonicalSource(t *testing.T) {
	path := "../parser/testdata/checkout.art"
	canon, _, doc, ok := encoded(t, path, read(t, path))
	if !ok {
		t.Fatalf("%s does not parse clean", path)
	}
	tree, err := Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := print.Canonical(tree); got != canon {
		t.Fatalf("the decoded tree does not print as the canonical source")
	}

	lines := strings.Split(canon, "\n")
	for _, d := range tree.Scenarios {
		s := d.Span()
		if s.File != path {
			t.Errorf("a scenario's span names %q, want %q", s.File, path)
		}
		if s.Line < 1 || s.Line > len(lines) {
			t.Fatalf("a scenario's span is on line %d of a %d-line file", s.Line, len(lines))
		}
		// The span's offset and its line and column have to agree, which is the
		// cheap check that these are real positions and not a plausible-looking
		// set of numbers.
		if got := canon[s.Offset:]; !strings.HasPrefix(got, "scenario") {
			t.Errorf("offset %d is %.20q, not the start of the scenario", s.Offset, got)
		}
		if !strings.HasPrefix(lines[s.Line-1][s.Col-1:], "scenario") {
			t.Errorf("line %d col %d is not the start of the scenario", s.Line, s.Col)
		}
	}
}

// TestDecodeRecomputesTheCheckersLabels: class, stepType and scope are ignored
// on the way in and recomputed from the tree, so a document carrying a wrong one
// cannot poison what a second client reads back.
func TestDecodeRecomputesTheCheckersLabels(t *testing.T) {
	path := "../parser/testdata/checkout.art"
	_, _, doc := mustEncode(t, path)

	tampered := strings.NewReplacer(
		`"class": "simple"`, `"class": "complex"`,
		`"stepType": "api"`, `"stepType": "terminal"`,
	).Replace(string(doc))
	if tampered == string(doc) {
		t.Fatalf("%s has neither a simple expect nor an api step, so this test asserts nothing", path)
	}

	tree, err := Decode([]byte(tampered))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := check.Check(tree)
	again, err := Encode(tree, info)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(doc) {
		t.Errorf("a tampered label survived the round trip\n%s", firstDifference(string(doc), string(again)))
	}
}

// mustEncode is encoded for a fixture that has to parse clean.
func mustEncode(t *testing.T, path string) (string, string, []byte) {
	t.Helper()
	src := read(t, path)
	canon, _, doc, ok := encoded(t, path, src)
	if !ok {
		t.Fatalf("%s does not parse clean", path)
	}
	return src, canon, doc
}
