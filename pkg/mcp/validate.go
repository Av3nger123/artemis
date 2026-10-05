package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// validateIn is source text and a label for the spans.
//
// File is never opened. It is what the diagnostics' spans name, so that an
// agent checking text it has not written yet still gets spans it recognises.
type validateIn struct {
	Source string `json:"source" jsonschema:"the .art source text to check"`
	File   string `json:"file,omitempty" jsonschema:"a label for the diagnostics' spans; this file is never opened"`
}

// sourceLabel is what a span names when the caller gave no file.
//
// It is deliberately not a path. A default of "scenario.art" would send a
// reader looking for a file that does not exist.
const sourceLabel = "<source>"

func addValidate(s *mcp.Server, srv *server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "artemis_validate",
		Description: "Check .art source text and return every diagnostic: the stable " +
			"code, the severity, the span, the message, a hint, and mechanical " +
			"suggestions whose `replace` is a ready fix. Nothing is read from or " +
			"written to disk -- pass the source you are about to write. Call this " +
			"after each change until `ok` is true. Key your behaviour off `code`, " +
			"which is stable; `message` is not.",
		Annotations:  readOnly(),
		OutputSchema: objectSchema(),
	}, srv.validateTool)
}

// validateTool runs the front end over the source and reports what it found.
//
// diag.JSONString is the writer `artemis parse --json` uses. Calling it rather
// than marshalling the diagnostics here is what makes the two clients agree,
// and a test asserts the bytes match over every invalid fixture in the corpus.
//
// The two channels carry deliberately different things, and the difference is
// the byte-identical promise:
//
//   - The text block is the diagnostics document exactly as pkg/dsl/diag wrote
//     it -- the same bytes as `artemis parse -f x.art --json`, and nothing
//     added. That is the channel the parity test reads.
//   - Structured content is that document with `ok` beside it. `ok` is this
//     tool's own field, not the checker's: an agent needs one boolean to loop
//     on, and asking it to infer that from an empty array is asking it to
//     re-derive what HasErrors already knows. It cannot go in the text block
//     without making these bytes something the CLI never prints.
func (srv *server) validateTool(ctx context.Context, req *mcp.CallToolRequest, in validateIn) (*mcp.CallToolResult, json.RawMessage, error) {
	label := in.File
	if label == "" {
		label = sourceLabel
	}

	_, _, bag := front.Compile(label, in.Source)
	doc := diag.JSONString(bag.All())

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &envelope); err != nil {
		return nil, nil, fmt.Errorf("the diagnostics document did not parse: %w", err)
	}
	ok, err := json.Marshal(!bag.HasErrors())
	if err != nil {
		return nil, nil, fmt.Errorf("encoding ok: %w", err)
	}
	envelope["ok"] = ok

	structured, err := json.Marshal(envelope)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the result: %w", err)
	}
	return document([]byte(doc)), structured, nil
}
