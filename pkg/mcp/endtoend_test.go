package mcpserver_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The loop an agent actually runs: find the scenarios, check the source it
// holds, format it, and check the result again. Each tool is tested on its own
// elsewhere; this asserts they compose, and that the output of one is accepted
// as the input of the next.
func TestListValidateFormatLoop(t *testing.T) {
	root := t.TempDir()
	src := "scenario    \"checkout\" {\n  step \"s\" {\n    get \"http://example.test\"\n    expect status == 200\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(root, "checkout.art"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	cs := connect(t, root)

	// 1. What is here?
	files := listFiles(t, cs, map[string]any{})
	if len(files) != 1 || files[0] != "checkout.art" {
		t.Fatalf("files = %v, want [checkout.art]", files)
	}

	// 2. Is the source the agent holds valid?
	checked := validate(t, cs, map[string]any{"source": src, "file": files[0]})
	if checked["ok"] != true {
		t.Fatalf("ok = %v, want true; diagnostics: %v", checked["ok"], checked["diagnostics"])
	}

	// 3. Make it canonical before writing it.
	_, formatted := formatSource(t, cs, src)
	if formatted["ok"] != true {
		t.Fatalf("format ok = %v, want true; diagnostics: %v", formatted["ok"], formatted["diagnostics"])
	}
	canonical, ok := formatted["source"].(string)
	if !ok {
		t.Fatalf("formatted source is not a string: %v", formatted["source"])
	}
	if canonical == src {
		t.Fatal("the fixture was already canonical; it exists to need formatting")
	}

	// 4. The formatted source still validates. If it did not, this loop would
	// be telling the agent to write a file the runner then refuses.
	again := validate(t, cs, map[string]any{"source": canonical, "file": files[0]})
	if again["ok"] != true {
		t.Fatalf("the formatted source does not validate: %v", again["diagnostics"])
	}

	// 5. The grammar is reachable in the same session, so an agent that gets
	// stuck mid-loop can ask what the language permits without reconnecting.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "artemis_grammar", Arguments: map[string]any{},
	})
	if err != nil || res.IsError {
		t.Fatalf("artemis_grammar failed in the same session: %v %+v", err, res)
	}
}
