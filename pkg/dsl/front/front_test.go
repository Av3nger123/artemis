package front_test

import (
	"os"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/front"
)

// Compile must report the checker's findings and not only the parser's. The
// fixture parses and then fails the checker, so a Compile that forgot to merge
// the checker's bag would return no diagnostics at all.
func TestCompileMergesTheCheckersBag(t *testing.T) {
	path := filepath.Join("..", "testdata", "invalid", "action_not_first.art")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	tree, info, bag := front.Compile(path, string(src))

	if tree == nil {
		t.Fatal("Compile returned no tree; the fixture is meant to parse")
	}
	if info == nil {
		t.Fatal("Compile returned no checker Info")
	}
	if !bag.HasErrors() {
		t.Fatalf("Compile found no errors in %s, which exists to have one", path)
	}
}

func TestIsArtFileIgnoresCase(t *testing.T) {
	for _, path := range []string{"x.art", "x.ART", "dir/x.Art"} {
		if !front.IsArtFile(path) {
			t.Errorf("IsArtFile(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"x.yaml", "x", "x.artemis"} {
		if front.IsArtFile(path) {
			t.Errorf("IsArtFile(%q) = true, want false", path)
		}
	}
}
