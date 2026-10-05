package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"artemis/pkg/dsl/grammar"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// grammarIn has no fields: the grammar does not depend on anything the caller
// knows. The type exists because the SDK infers the input schema from it.
type grammarIn struct{}

// addGrammar registers artemis_grammar.
//
// The description is the agent's only instruction about when to reach for this,
// so it says what the document is for rather than what it contains.
//
// srv is taken for the same reason every other add* function takes it -- so the
// set of tools is registered one way -- even though the grammar is in the
// binary and needs nothing from the workspace. grammarTool stays a plain
// function rather than becoming a method with an unused receiver.
func addGrammar(s *mcp.Server, srv *server) {
	_ = srv
	mcp.AddTool(s, &mcp.Tool{
		Name: "artemis_grammar",
		Description: "Every finite option set in the Artemis DSL: the step types, " +
			"the fields each action block takes, the assertion operators, and the " +
			"values each field accepts. Call this before writing a .art file to " +
			"learn what the language permits instead of guessing.",
		Annotations:  readOnly(),
		OutputSchema: objectSchema(),
	}, grammarTool)
}

// grammarTool hands through grammar.JSON() unchanged.
//
// json.RawMessage keeps the bytes the grammar package wrote rather than
// rebuilding them: a round trip through map[string]any would reorder keys and
// quietly make this a second description of the language.
//
// The document goes back on both channels, and the reason is the byte-identical
// promise. Structured content is the useful shape for a client that wants
// fields, but the protocol decodes it into a map on the way, so key order does
// not survive it. The text block is the document exactly as the grammar package
// wrote it, which is what makes "the same bytes as `artemis grammar --json`"
// true of something the caller actually receives.
func grammarTool(ctx context.Context, req *mcp.CallToolRequest, in grammarIn) (*mcp.CallToolResult, json.RawMessage, error) {
	doc, err := grammar.JSON()
	if err != nil {
		return nil, nil, fmt.Errorf("building the grammar document: %w", err)
	}
	return document(doc), json.RawMessage(doc), nil
}
