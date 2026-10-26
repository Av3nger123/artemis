package shared

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// linked are the documents whose relative links are checked: everything a
// reader moves between, so a page that moves or a heading that is renamed
// fails here rather than leaving a link that goes nowhere.
func linked(t *testing.T) []string {
	t.Helper()

	docs := guides(t)
	for _, name := range []string{"SPEC.md", "ROADMAP.md", "CONTRIBUTING.md", "SECURITY.md"} {
		docs = append(docs, filepath.Join("../..", name))
	}
	return docs
}

// mdLink is a markdown link's target, `[text](target)`. An image is a link
// too, and is checked the same way.
var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// TestRelativeLinksResolve holds every relative link in the documents to a
// file that exists, and every #anchor to a heading in the file it names.
// Links to the web are left alone: whether they resolve is not this
// repository's to say.
func TestRelativeLinksResolve(t *testing.T) {
	anchors := map[string]map[string]bool{}
	anchorsOf := func(path string) map[string]bool {
		if a, ok := anchors[path]; ok {
			return a
		}
		a := headingAnchors(docText(t, path))
		anchors[path] = a
		return a
	}

	checked := 0
	for _, doc := range linked(t) {
		for i, line := range proseLines(docText(t, doc)) {
			for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				checked++
				file, anchor, _ := strings.Cut(target, "#")
				path := doc
				if file != "" {
					path = filepath.Join(filepath.Dir(doc), file)
					if _, err := os.Stat(path); err != nil {
						t.Errorf("%s line %d: %s links to %s, which does not exist", doc, i+1, target, file)
						continue
					}
				}
				if anchor == "" || !strings.HasSuffix(path, ".md") {
					continue
				}
				if !anchorsOf(path)[anchor] {
					t.Errorf("%s line %d: %s links to #%s, which is no heading in %s", doc, i+1, target, anchor, filepath.Base(path))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no relative links found: the link pattern no longer matches the documents")
	}
}

// proseLines is the document's lines with every fenced block blanked, so an
// example that happens to contain "](" is not read as a link and line numbers
// still match the file.
func proseLines(text string) []string {
	lines := strings.Split(text, "\n")
	fence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			lines[i] = ""
			continue
		}
		if fence {
			lines[i] = ""
		}
	}
	return lines
}

// headingAnchors is the set of anchors GitHub gives a document's headings:
// lower-cased, with everything but letters, digits, spaces, hyphens and
// underscores dropped, spaces turned into hyphens, and a repeated anchor
// numbered -1, -2 and so on.
func headingAnchors(text string) map[string]bool {
	out := map[string]bool{}
	seen := map[string]int{}
	for _, line := range proseLines(text) {
		trimmed := strings.TrimLeft(line, "#")
		if trimmed == line || !strings.HasPrefix(trimmed, " ") {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(trimmed)) {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
				b.WriteRune(r)
			case r == ' ':
				b.WriteRune('-')
			}
		}
		slug := b.String()
		if n := seen[slug]; n > 0 {
			out[slug+"-"+itoa(n)] = true
		} else {
			out[slug] = true
		}
		seen[slug]++
	}
	return out
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + itoa(n%10)
}
