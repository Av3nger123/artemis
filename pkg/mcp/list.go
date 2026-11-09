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
	"artemis/pkg/dsl/token"

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
			"`name.item` refs each collection file's requests are used by: name is the collection's id, which " +
			"`import \"<file>\"` gives it, and under `collection_labels` the label of each item that has " +
			"one, by its ref. Under `scenario_files`, each scenario file -- one whose " +
			"first line is `flow \"<path>\"` -- with the flow file its header names (workspace-relative), " +
			"the flow's id and `flow_label`, the ids of its scenarios, and under `scenario_labels` the " +
			"label of each that has one. Show a label where there is one, and the id otherwise. Call this first to find out what " +
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
	collectionLabels := map[string]map[string]string{}
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
		if items, labels := srv.collectionItems(rel); len(items) > 0 {
			collections[rel] = items
			if len(labels) > 0 {
				collectionLabels[rel] = labels
			}
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
	if len(collectionLabels) > 0 {
		out["collection_labels"] = collectionLabels
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
// order, and the label of each that has one, by the same key: name is the
// collection's id, which is what importing the file with no as gives it. An
// old collection, titled in quotes, is named by its file, or by what an `as`
// should be when the file's name cannot be one. The file is parsed and not expanded: what a file declares is
// read off what it says, and a file that does not compile still lists what
// did parse. A file that cannot be read lists nothing; artemis_validate is
// where its problems are reported.
func (srv *server) collectionItems(rel string) ([]string, map[string]string) {
	// Through resolveArt, not the walked path: a symlink to a file outside the
	// workspace is listed as a file but never read.
	abs, err := srv.ws.resolveArt(rel)
	if err != nil {
		return nil, nil
	}
	b, err := os.ReadFile(abs) //nolint:gosec // resolveArt kept it inside the workspace
	if err != nil {
		return nil, nil
	}
	tree, _ := parser.Parse(rel, string(b))
	var out []string
	labels := map[string]string{}
	for _, d := range tree.Scenarios {
		c, ok := d.(*ast.Collection)
		if !ok {
			continue
		}
		name := c.Name.Value
		if c.Name.Kind != token.Ident {
			var ok bool
			if name, ok = parser.ImportName(rel); !ok {
				name = parser.IdentLike(name)
			}
		}
		for _, it := range c.Items {
			if r, ok := it.(*ast.RequestDecl); ok {
				ref := name + "." + r.Name.Text
				out = append(out, ref)
				if r.Label.Text != "" {
					labels[ref] = r.Label.Value
				}
			}
		}
	}
	return out, labels
}

// scenarioFile is what the list says about a scenario file: the flow its
// header names and the scenarios it holds.
type scenarioFile struct {
	// FlowFile is the header's path made workspace-relative, as every other
	// path the tools take is.
	FlowFile string `json:"flow_file"`
	// Flow is the flow's id, or "" when the flow file cannot be read or
	// declares no flow, and FlowLabel its label, omitted when it has none.
	Flow      string `json:"flow"`
	FlowLabel string `json:"flow_label,omitempty"`
	// Scenarios are the scenarios' ids, in order, and ScenarioLabels the
	// label of each that has one, by its id.
	Scenarios      []string          `json:"scenarios"`
	ScenarioLabels map[string]string `json:"scenario_labels,omitempty"`
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
			if sc.Label.Text != "" {
				if out.ScenarioLabels == nil {
					out.ScenarioLabels = map[string]string{}
				}
				out.ScenarioLabels[sc.Name.Value] = sc.Label.Value
			}
		}
	}
	// Through resolveArt, so a header that climbs out of the workspace names
	// a file that is never read.
	if flowAbs, err := srv.ws.resolveArt(out.FlowFile); err == nil {
		if src, err := os.ReadFile(flowAbs); err == nil { //nolint:gosec // as above
			if f, isFlow := front.Flow(out.FlowFile, string(src)); isFlow {
				out.Flow, out.FlowLabel = f.Name.Value, f.Label.Value
			}
		}
	}
	return out, true
}
