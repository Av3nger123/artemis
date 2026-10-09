package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/print"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type formatIn struct {
	Source string `json:"source" jsonschema:"the .art source text to format"`
	File   string `json:"file,omitempty" jsonschema:"a label for the diagnostics' spans; this file is never opened"`
}

func addFormat(s *mcp.Server, srv *server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "artemis_format",
		Description: "Return .art source text in canonical layout: the exact bytes " +
			"`artemis fmt` writes. Nothing is read from or written to disk. Call " +
			"this on source that already validates, before writing the file, so " +
			"the file on disk is fmt-clean and a later edit is a one-line diff. " +
			"Source that does not compile has no canonical form and comes back " +
			"with its diagnostics instead.",
		Annotations:  readOnly(),
		OutputSchema: objectSchema(),
	}, srv.formatTool)
}

// formatTool prints the tree canonically, or explains why it cannot.
//
// The order matters: errors first, before anything is printed. Source with an
// error has no canonical form, and a printer run over a partial tree would
// return something that looks like a formatted file and is missing the parts
// the parser could not read.
//
// On success the text block is the canonical source itself -- the bytes to write
// to the file -- rather than a JSON document. That is what a caller wants from
// this tool, and structured content still carries the same source under `source`
// for a client that reads fields.
func (srv *server) formatTool(ctx context.Context, req *mcp.CallToolRequest, in formatIn) (*mcp.CallToolResult, json.RawMessage, error) {
	label := in.File
	if label == "" {
		label = sourceLabel
	}

	// The workspace loader is for the diagnostics only: an import in the file
	// must not read as an error here. The printer gets Tree, never Expanded.
	unit := front.CompileWith(label, in.Source, srv.ws.loader())
	tree, bag := unit.Tree, unit.Bag
	if bag.HasErrors() {
		doc := diag.JSONString(bag.All())
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(doc), &envelope); err != nil {
			return nil, nil, fmt.Errorf("the diagnostics document did not parse: %w", err)
		}
		envelope["ok"] = json.RawMessage("false")
		structured, err := json.Marshal(envelope)
		if err != nil {
			return nil, nil, fmt.Errorf("encoding the result: %w", err)
		}
		return document([]byte(doc)), structured, nil
	}

	formatted := print.Canonical(tree)
	structured, err := json.Marshal(map[string]any{"ok": true, "source": formatted})
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the result: %w", err)
	}
	return document([]byte(formatted)), structured, nil
}
