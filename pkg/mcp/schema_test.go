package mcpserver_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var update = flag.Bool("update", false, "rewrite the golden file")

// The tool list is the wire contract: the names, the descriptions, the
// annotations, and the input schemas the SDK infers from the handler structs.
// It is committed so that a struct-tag edit is visible in review rather than
// shipping as a silent change to what an agent is told.
//
// pkg/dsl/encode/testdata/schema.json gets the same treatment for the same
// reason: nobody reads a generated artifact, so it goes stale and then pins the
// wrong thing with confidence.
func TestToolSchemasMatchTheGolden(t *testing.T) {
	cs := connect(t, t.TempDir())
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}

	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatalf("encoding the tools: %v", err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "tools.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("making testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatalf("writing %s: %v", golden, err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run `go test ./pkg/mcp -update` to create it): %v", golden, err)
	}
	if string(got) != string(want) {
		t.Errorf("the tool list changed.\n got:\n%s\nwant:\n%s", got, want)
	}
}
