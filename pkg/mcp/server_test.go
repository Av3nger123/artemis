package mcpserver_test

import (
	"context"
	"encoding/json"
	"testing"

	mcpserver "artemis/pkg/mcp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect starts a server over an in-memory transport and returns a client
// session for a test to call tools on. Every test in this package goes through
// here, so none of them can accidentally exercise a differently-built server.
func connect(t *testing.T, workspace string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := mcpserver.New(mcpserver.Options{Workspace: workspace})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)

	st, ct := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("connecting the server: %v", err)
	}
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("connecting the client: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close() //nolint:errcheck // the test is over
		serverSession.Wait()  //nolint:errcheck // the test is over
	})
	return clientSession
}

// The grammar tool must hand through grammar.JSON() rather than a second
// description of the language, so the assertion is that the payload parses as
// the grammar document and is not empty.
func TestGrammarToolReturnsTheGrammarDocument(t *testing.T) {
	cs := connect(t, t.TempDir())

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "artemis_grammar",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("calling artemis_grammar: %v", err)
	}
	if res.IsError {
		t.Fatalf("artemis_grammar reported an error: %+v", res.Content)
	}

	var got map[string]any
	if err := json.Unmarshal(documentBytes(t, res), &got); err != nil {
		t.Fatalf("the grammar payload is not JSON: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the grammar document is empty")
	}
}

// documentBytes is the document a tool returned, exactly as it wrote it.
//
// It reads the text block rather than StructuredContent on purpose. The
// protocol decodes structured content into a map, so key order does not survive
// it, and the whole point of these tools is that the bytes are the CLI's bytes.
func documentBytes(t *testing.T, res *mcp.CallToolResult) []byte {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("want exactly one content block, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("want a text content block, got %T", res.Content[0])
	}
	return []byte(text.Text)
}

// Every tool this version serves is read-only. A tool that claims otherwise, or
// claims nothing, gets gated differently by a host -- or not at all.
func TestEveryToolIsAnnotatedReadOnly(t *testing.T) {
	cs := connect(t, t.TempDir())

	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("the server registered no tools")
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations", tool.Name)
			continue
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("%s is not annotated closed-world", tool.Name)
		}
	}
}
