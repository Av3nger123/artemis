package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// call runs a tool and fails the test if the tool itself errored. Every tool
// test goes through here so none of them can read a result off a failed call.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s reported an error: %+v", name, res.Content)
	}
	return res
}

// structured is a tool's structured content, decoded. It is the convenience
// shape -- the envelope with `ok` -- and not the byte-exact document.
func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("re-encoding the structured content: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the structured content is not an object: %v", err)
	}
	return got
}

func validate(t *testing.T, cs *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	return structured(t, call(t, cs, "artemis_validate", args))
}

func TestValidateAcceptsCleanSource(t *testing.T) {
	cs := connect(t, t.TempDir())
	got := validate(t, cs, map[string]any{
		"source": "scenario \"ok\" {\n  step \"s\" {\n    get \"http://example.test\"\n    expect status == 200\n  }\n}\n",
	})
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true; diagnostics: %v", got["ok"], got["diagnostics"])
	}
}

func TestValidateRejectsSourceWithAnError(t *testing.T) {
	cs := connect(t, t.TempDir())
	got := validate(t, cs, map[string]any{"source": "scenario {\n"})
	if got["ok"] != false {
		t.Fatalf("ok = %v, want false", got["ok"])
	}
	if got["diagnostics"] == nil {
		t.Error("diagnostics are absent; the caller has nothing to act on")
	}
}

// With no file given, a span names the source label rather than a path that
// does not exist.
func TestValidateLabelsSpansWhenGivenNoFile(t *testing.T) {
	cs := connect(t, t.TempDir())
	res := call(t, cs, "artemis_validate", map[string]any{"source": "scenario {\n"})

	var doc struct {
		Diagnostics []struct {
			Span struct{ File string } `json:"span"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(documentBytes(t, res), &doc); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}
	if len(doc.Diagnostics) == 0 {
		t.Fatal("no diagnostics for source that does not compile")
	}
	if got := doc.Diagnostics[0].Span.File; got != "<source>" {
		t.Errorf("span file = %q, want %q", got, "<source>")
	}
}

// The parity gate of docs/artemis-mcp-client.md: for every fixture that exists
// to fail, the tool's document must be the bytes the CLI prints. This is the
// test that stops the two clients drifting.
func TestValidateMatchesParseJSONForEveryInvalidFixture(t *testing.T) {
	bin := buildArtemis(t)
	dir := filepath.Join("..", "dsl", "testdata", "invalid")
	fixtures, err := filepath.Glob(filepath.Join(dir, "*.art"))
	if err != nil {
		t.Fatalf("globbing the fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures in %s", dir)
	}

	cs := connect(t, t.TempDir())
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			src, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("reading %s: %v", fixture, err)
			}

			// The CLI's answer. parse --json exits non-zero on a file that does
			// not compile, which is the normal case here, so the exit status is
			// ignored and only the document is compared.
			cmd := exec.Command(bin, "parse", "-f", fixture, "--json")
			want, _ := cmd.Output()
			if len(want) == 0 {
				t.Fatalf("`artemis parse -f %s --json` wrote nothing", fixture)
			}

			// The tool's answer, with file set to the same path so the spans
			// name the same file.
			res := call(t, cs, "artemis_validate", map[string]any{
				"source": string(src),
				"file":   fixture,
			})
			got := documentBytes(t, res)

			if string(got) != string(want) {
				t.Errorf("the document differs from `artemis parse --json`\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// buildArtemis builds the binary once for the parity test. The test compares
// this package against the CLI, so it has to run the CLI.
func buildArtemis(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "artemis")
	cmd := exec.Command("go", "build", "-o", bin, "artemis/cmd/cli")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building artemis: %v\n%s", err, out)
	}
	return bin
}
