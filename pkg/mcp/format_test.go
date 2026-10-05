package mcpserver_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/print"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func formatSource(t *testing.T, cs *mcp.ClientSession, src string) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res := call(t, cs, "artemis_format", map[string]any{"source": src})
	return res, structured(t, res)
}

// The tool must return exactly what print.Canonical returns, because that is
// what `artemis fmt` writes and what the file on disk will be.
func TestFormatReturnsCanonicalSource(t *testing.T) {
	cs := connect(t, t.TempDir())
	src := "scenario   \"spaced\"    {\n  step \"s\" {\n    get \"http://example.test\"\n  }\n}\n"

	res, got := formatSource(t, cs, src)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true; diagnostics: %v", got["ok"], got["diagnostics"])
	}

	tree, _, _ := front.Compile("<source>", src)
	want := print.Canonical(tree)
	if string(documentBytes(t, res)) != want {
		t.Errorf("the text block is not the canonical source\n got: %q\nwant: %q", documentBytes(t, res), want)
	}
	if got["source"] != want {
		t.Errorf("structured source = %q, want %q", got["source"], want)
	}
}

// Formatting is idempotent, so the tool's own output must format to itself. An
// agent that writes this result has written a fmt-clean file.
func TestFormatIsIdempotent(t *testing.T) {
	cs := connect(t, t.TempDir())
	_, once := formatSource(t, cs, "scenario \"x\" {\n  step \"s\" {\n    get \"http://example.test\"\n  }\n}\n")
	first, ok := once["source"].(string)
	if !ok {
		t.Fatalf("source is not a string: %v", once["source"])
	}
	_, twice := formatSource(t, cs, first)
	if twice["source"] != first {
		t.Errorf("formatting twice changed the source:\n once: %q\ntwice: %q", first, twice["source"])
	}
}

// Source that does not compile has no canonical form. The tool says why rather
// than returning something that looks like a formatted file.
func TestFormatRefusesSourceThatDoesNotCompile(t *testing.T) {
	cs := connect(t, t.TempDir())
	_, got := formatSource(t, cs, "scenario {\n")
	if got["ok"] != false {
		t.Fatalf("ok = %v, want false", got["ok"])
	}
	if got["source"] != nil {
		t.Errorf("source = %v, want absent for source that does not compile", got["source"])
	}
	if got["diagnostics"] == nil {
		t.Error("diagnostics are absent; the caller has nothing to act on")
	}
}

// The parity claim for this tool: the bytes it returns are the bytes
// `artemis fmt` writes. Asserted against the real command over the corpus
// fixture the formatter's own goldens are built from.
func TestFormatMatchesTheFmtCommand(t *testing.T) {
	bin := buildArtemis(t)
	fixture := filepath.Join("..", "dsl", "grammar", "example.art")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("reading %s: %v", fixture, err)
	}

	want, err := exec.Command(bin, "fmt", fixture).Output()
	if err != nil {
		t.Fatalf("`artemis fmt %s`: %v", fixture, err)
	}

	cs := connect(t, t.TempDir())
	res, got := formatSource(t, cs, string(src))
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true; diagnostics: %v", got["ok"], got["diagnostics"])
	}
	if string(documentBytes(t, res)) != string(want) {
		t.Errorf("the tool and `artemis fmt` disagree\n got: %q\nwant: %q", documentBytes(t, res), want)
	}
}
