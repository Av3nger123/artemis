// Package mcpserver is the MCP client of docs/artemis-mcp-client.md: a stdio
// server that lets an agent author .art files and, later, run them.
//
// Every tool here hands through a document some other package owns --
// grammar.JSON, diag.JSON, print.Canonical -- rather than describing a file in
// its own words. That is the whole design: the CLI and this server are two
// callers of the same writers, so they cannot disagree about what a file means.
//
// The package is named mcpserver rather than mcp because every file in it also
// imports the SDK's own mcp package.
package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is what the server reports as its implementation version. It is the
// protocol's idea of a version and not artemis's: a host shows it when it lists
// the servers it knows about.
const Version = "v1"

// Options is what the server needs to exist. Workspace is the directory every
// relative path a tool is given resolves against, and the boundary no tool may
// read outside of.
type Options struct {
	Workspace string
}

// server is what the tool handlers close over: the workspace, and nothing else.
// There is no session and no cache, so a restart loses nothing.
type server struct {
	ws workspace
}

// New builds the server with every tool registered. It does not connect: the
// caller chooses the transport, which is StdioTransport for `artemis mcp` and
// an in-memory pair for a test.
//
// A workspace that will not resolve leaves srv.ws zero, and a zero workspace
// refuses every path -- filepath.Join("", rel) cannot carry the prefix "" plus
// a separator. That is deliberate rather than fatal: the server still starts
// and artemis_grammar still answers, which is a server a host can show an error
// from, instead of a process that exited before it could say why.
func New(opts Options) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "artemis", Version: Version}, nil)

	ws, _ := newWorkspace(opts.Workspace)
	srv := &server{ws: ws}

	addGrammar(s, srv)
	return s
}

// readOnly is the annotation every tool in this version carries.
//
// It is one function rather than a literal per tool because the hint is a
// security property and not decoration: a host cannot gate what it was not told
// about, and the tool that got missed would be the one that reads something it
// should not. OpenWorldHint is a pointer in the SDK and defaults to true, so
// saying "closed" means setting it explicitly.
func readOnly() *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closed}
}

// document is the tool result that carries a document's exact bytes.
//
// Every tool here returns the document twice: as structured content, which the
// protocol decodes into a map and whose key order therefore does not survive
// the trip, and as this text block, which does. The byte-identical promise in
// docs/artemis-mcp-client.md is about what the caller receives, so it has to be
// kept on a channel that preserves bytes.
func document(doc []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(doc)}}}
}

// objectSchema is the output schema every tool here declares.
//
// The SDK infers a tool's output schema from its handler's return type, and
// these handlers return json.RawMessage so that the document's bytes survive
// untouched. RawMessage is []byte, which infers to a string schema, and AddTool
// refuses any output schema that is not an object -- so the schema is stated
// here instead. Declaring it is the cheaper half of the trade: the alternative
// is returning map[string]any and losing the byte-for-byte property that is the
// reason these tools exist.
//
// It is deliberately open: the documents it describes belong to pkg/dsl/grammar
// and pkg/dsl/diag, and restating their fields here would be a second schema to
// keep in step with them.
func objectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object"}
}
