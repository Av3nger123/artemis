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
			"`collection.item` names each file declares (for a `use`). Call this first to find out what " +
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
		if items := collectionItems(p, rel); len(items) > 0 {
			collections[rel] = items
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
	doc, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the result: %w", err)
	}
	return document(doc), doc, nil
}

// collectionItems is every `collection.item` the file at abs declares, in
// source order. The file is parsed and not expanded: what a file declares is
// read off what it says, and a file that does not compile still lists what
// did parse. A file that cannot be read lists nothing; artemis_validate is
// where its problems are reported.
func collectionItems(abs, rel string) []string {
	b, err := os.ReadFile(abs) //nolint:gosec // the walk stayed inside the workspace
	if err != nil {
		return nil
	}
	tree, _ := parser.Parse(rel, string(b))
	var out []string
	for _, d := range tree.Scenarios {
		c, ok := d.(*ast.Collection)
		if !ok {
			continue
		}
		for _, it := range c.Items {
			switch it := it.(type) {
			case *ast.RequestDecl:
				out = append(out, c.Name.Value+"."+it.Name.Text)
			case *ast.FlowDecl:
				out = append(out, c.Name.Value+"."+it.Name.Text)
			}
		}
	}
	return out
}
