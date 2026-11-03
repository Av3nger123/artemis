package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/parser"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listIn struct {
	Dir string `json:"dir,omitempty" jsonschema:"a workspace-relative directory to walk; the whole workspace when absent"`
}

func addList(s *mcp.Server, srv *server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "artemis_list",
		Description: "List the .art scenario files in the workspace, in path order, " +
			"as workspace-relative paths, and under `collections` the " +
			"`name.item` refs each collection file's requests are used by: name is the file's name, which " +
			"`import \"<file>\"` gives it, or, for a file whose name cannot be one, the name to give it " +
			"with `import \"<file>\" as name`. Under `scenario_files`, each scenario file -- one whose " +
			"first line is `flow \"<path>\"` -- with the flow file its header names (workspace-relative), " +
			"the flow's name, and the titles of its scenarios. Call this first to find out what " +
			"scenarios exist before reading or changing one.",
		Annotations:  readOnly(),
		OutputSchema: objectSchema(),
	}, srv.listTool)
}

// listTool walks the directory and returns the scenarios under it.
//
// The walk mirrors `artemis run <folder>` on purpose. filepath.WalkDir visits
// lexically, so two calls on one tree give the same order on every machine, and
// a directory whose name starts with a dot is skipped because .git and .github
// hold no scenarios. A list that disagreed with what `artemis run` picks up
// would be a second answer about the same tree.
func (srv *server) listTool(ctx context.Context, req *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, json.RawMessage, error) {
	root, err := srv.ws.resolve(in.Dir)
	if err != nil {
		return nil, nil, err
	}

	files := []string{}
	collections := map[string][]string{}
	scenarioFiles := map[string]scenarioFile{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// The root is walked whatever it is called: a caller who asks for
			// ./.suite means it.
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !front.IsArtFile(d.Name()) {
			return nil
		}
		// Relative to the workspace and not to the directory walked: every
		// other tool takes a workspace-relative path, so these can be handed
		// straight back to one.
		rel, relErr := filepath.Rel(srv.ws.root, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, rel)
		if items := srv.collectionItems(rel); len(items) > 0 {
			collections[rel] = items
		}
		if sf, ok := srv.scenarioFile(rel); ok {
			scenarioFiles[rel] = sf
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walking %s: %w", in.Dir, err)
	}

	out := map[string]any{"workspace": srv.ws.root, "files": files}
	if len(collections) > 0 {
		out["collections"] = collections
	}
	if len(scenarioFiles) > 0 {
		out["scenario_files"] = scenarioFiles
	}
	doc, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the result: %w", err)
	}
	return document(doc), doc, nil
}

// collectionItems is every `name.item` the file at abs declares, in source
// order: name is what importing the file gives its collection, or what an
// `as` should when the file's name cannot be one. The file is parsed and not expanded: what a file declares is
// read off what it says, and a file that does not compile still lists what
// did parse. A file that cannot be read lists nothing; artemis_validate is
// where its problems are reported.
func (srv *server) collectionItems(rel string) []string {
	// Through resolveArt, not the walked path: a symlink to a file outside the
	// workspace is listed as a file but never read.
	abs, err := srv.ws.resolveArt(rel)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(abs) //nolint:gosec // resolveArt kept it inside the workspace
	if err != nil {
		return nil
	}
	tree, _ := parser.Parse(rel, string(b))
	name, ok := parser.ImportName(rel)
	if !ok {
		name = parser.IdentLike(name)
	}
	var out []string
	for _, d := range tree.Scenarios {
		c, ok := d.(*ast.Collection)
		if !ok {
			continue
		}
		for _, it := range c.Items {
			if r, ok := it.(*ast.RequestDecl); ok {
				out = append(out, name+"."+r.Name.Text)
			}
		}
	}
	return out
}

// scenarioFile is what the list says about a scenario file: the flow its
// header names and the scenarios it holds.
type scenarioFile struct {
	// FlowFile is the header's path made workspace-relative, as every other
	// path the tools take is.
	FlowFile string `json:"flow_file"`
	// Flow is the flow's name, or "" when the flow file cannot be read or
	// declares no flow.
	Flow      string   `json:"flow"`
	Scenarios []string `json:"scenarios"`
}

// scenarioFile reads the file at rel as a scenario file. ok is false for a
// file with no `flow "..."` header. Like collectionItems, it reads what the
// file says and does not expand it.
func (srv *server) scenarioFile(rel string) (scenarioFile, bool) {
	abs, err := srv.ws.resolveArt(rel)
	if err != nil {
		return scenarioFile{}, false
	}
	b, err := os.ReadFile(abs) //nolint:gosec // resolveArt kept it inside the workspace
	if err != nil {
		return scenarioFile{}, false
	}
	header, ok := front.FlowHeader(rel, string(b))
	if !ok {
		return scenarioFile{}, false
	}
	out := scenarioFile{
		FlowFile:  filepath.Join(filepath.Dir(rel), filepath.FromSlash(header)),
		Scenarios: []string{},
	}
	tree, _ := parser.Parse(rel, string(b))
	for _, d := range tree.Scenarios {
		if sc, isScenario := d.(*ast.Scenario); isScenario {
			out.Scenarios = append(out.Scenarios, sc.Name.Value)
		}
	}
	// Through resolveArt, so a header that climbs out of the workspace names
	// a file that is never read.
	if flowAbs, err := srv.ws.resolveArt(out.FlowFile); err == nil {
		if src, err := os.ReadFile(flowAbs); err == nil { //nolint:gosec // as above
			if _, name, isFlow := front.FlowDecl(out.FlowFile, string(src)); isFlow {
				out.Flow = name
			}
		}
	}
	return out, true
}
