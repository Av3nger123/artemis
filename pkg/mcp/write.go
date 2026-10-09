package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/print"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type writeIn struct {
	Path   string `json:"path" jsonschema:"a workspace-relative .art path to write"`
	Source string `json:"source" jsonschema:"the .art source text to write"`
}

func addWrite(s *mcp.Server, srv *server) {
	// Not read-only, and not open-world the way artemis_run is: this writes one
	// .art file inside the workspace and runs nothing. No network, no commands.
	destructive := true
	open := false
	mcp.AddTool(s, &mcp.Tool{
		Name: "artemis_write",
		Description: "Write .art source to a workspace-relative path, after checking " +
			"it and putting it in canonical layout. Source that does not compile is " +
			"refused with its diagnostics and nothing is written, so this tool " +
			"cannot leave a broken scenario on disk. The file written is always " +
			"fmt-clean. An existing file is overwritten; `created` says which " +
			"happened.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: &destructive,
			OpenWorldHint:   &open,
		},
		OutputSchema: objectSchema(),
	}, srv.writeTool)
}

// writeTool checks the source, makes it canonical, and writes it.
//
// Checking first is the whole character of this tool. The one thing a writer
// into somebody's repo must not do is leave a file the runner then refuses, so
// source with an error comes back as diagnostics and the disk is untouched.
// That also means every file this tool writes is fmt-clean, which is the
// precondition the canonical-save decision needs: the next edit to it is a
// one-line diff rather than a whole-file reformat.
//
// It writes the canonical form rather than the bytes it was handed, for the
// same reason. An agent that wanted its own spacing kept is asking for the
// thing the formatter exists to take away.
func (srv *server) writeTool(ctx context.Context, req *mcp.CallToolRequest, in writeIn) (*mcp.CallToolResult, json.RawMessage, error) {
	abs, err := srv.ws.resolveNew(in.Path)
	if err != nil {
		return nil, nil, err
	}

	// The span label is the path the caller asked for, so a diagnostic names
	// the file they were trying to create.
	unit := front.CompileWith(in.Path, in.Source, srv.ws.loader())
	tree, bag := unit.Tree, unit.Bag
	if bag.HasErrors() {
		doc := diag.JSONString(bag.All())
		var envelope map[string]json.RawMessage
		if uErr := json.Unmarshal([]byte(doc), &envelope); uErr != nil {
			return nil, nil, fmt.Errorf("the diagnostics document did not parse: %w", uErr)
		}
		envelope["ok"] = json.RawMessage("false")
		envelope["written"] = json.RawMessage("false")
		structured, mErr := json.Marshal(envelope)
		if mErr != nil {
			return nil, nil, fmt.Errorf("encoding the result: %w", mErr)
		}
		return document([]byte(doc)), structured, nil
	}

	// Whether this is a new file is read before the write, not after it.
	created := false
	if _, statErr := os.Stat(abs); os.IsNotExist(statErr) {
		created = true
	}

	formatted := print.Canonical(tree)
	if wErr := os.WriteFile(abs, []byte(formatted), 0o600); wErr != nil {
		return nil, nil, fmt.Errorf("writing %s: %w", in.Path, wErr)
	}

	structured, err := json.Marshal(map[string]any{
		"ok":      true,
		"written": true,
		"created": created,
		"path":    in.Path,
		"source":  formatted,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the result: %w", err)
	}
	return document([]byte(formatted)), structured, nil
}
