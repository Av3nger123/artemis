package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runIn struct {
	Path    string `json:"path" jsonschema:"a workspace-relative .art file, or a folder of them"`
	EnvFile string `json:"envFile,omitempty" jsonschema:"a workspace-relative env file to load before the run"`
}

func addRun(s *mcp.Server, srv *server) {
	// The only tool here that is not read-only, and the annotations say so. A
	// terminal step runs a shell command and a browser step drives a real
	// browser, so this executes whatever the scenario says and talks to
	// whatever it names. A host cannot gate what it was not told about.
	destructive := true
	open := true
	mcp.AddTool(s, &mcp.Tool{
		Name: "artemis_run",
		Description: "Run the scenarios in a workspace-relative .art file, or in a " +
			"folder of them, and return the full JSON report: counts, every " +
			"scenario and step, every assertion, and a failure block naming what " +
			"was expected and what happened. Read `passed` -- a run whose " +
			"assertions failed is a successful call reporting a failed test, not " +
			"an error. WARNING: this executes the scenario. A terminal step runs a " +
			"shell command, a browser step drives a browser, and an api step sends " +
			"real requests to whatever URL it names.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: &destructive,
			OpenWorldHint:   &open,
		},
		OutputSchema: objectSchema(),
	}, srv.runTool)
}

// runTool runs the scenarios by running artemis.
//
// It executes the binary rather than calling a run engine in process, and that
// is a deliberate reversal of this design's first draft. The principle the
// draft was protecting -- that no second piece of code decides what a file
// means -- is kept exactly: `--report json` is written by pkg/report, so the
// document this returns is the document the CLI prints, from the same code.
// What the draft actually ruled out was a subprocess per *validation*, in the
// authoring loop; artemis_validate has no subprocess, and a run that sends real
// requests does not notice a few milliseconds of process start.
//
// The alternative was extracting the run engine out of pkg/cli, where it is
// unexported. That is a large refactor with no user-visible result, and it buys
// in-process cancellation and nothing else. Worth doing the day something needs
// it, and not before.
func (srv *server) runTool(ctx context.Context, req *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, json.RawMessage, error) {
	target, err := srv.ws.resolveRunTarget(in.Path)
	if err != nil {
		return nil, nil, err
	}

	// The run is a subprocess that reads imports from disk on its own, so the
	// workspace boundary has to be enforced here, before it starts: every
	// scenario it would load is compiled through the workspace loader first, and
	// an import that cannot be read through it stops the run.
	if err := srv.checkImports(target); err != nil {
		return nil, nil, fmt.Errorf("running %s: %w", in.Path, err)
	}

	args := []string{"run", target, "--report", "json"}
	if in.EnvFile != "" {
		envFile, envErr := srv.ws.resolve(in.EnvFile)
		if envErr != nil {
			return nil, nil, envErr
		}
		args = append(args, "--env", envFile)
	}

	cmd := exec.CommandContext(ctx, srv.bin, args...)
	// The workspace, so a scenario's relative paths and a default .env resolve
	// the way they would for somebody running artemis there by hand.
	cmd.Dir = srv.ws.root
	// Only stdout is the document. The console report moves to stderr when a
	// report goes to stdout, which is what keeps stdout exactly one document.
	doc, runErr := cmd.Output()

	// A failed run exits non-zero, and that is not a tool error. The CLI's exit
	// code has no equivalent here, and a red test arriving as a broken tool
	// would have the agent trying to repair the runner. The document is the
	// answer, so a non-zero exit that produced one is a success.
	if len(doc) == 0 {
		if runErr != nil {
			return nil, nil, fmt.Errorf("running %s: %w", in.Path, runErr)
		}
		return nil, nil, fmt.Errorf("running %s: artemis wrote no report", in.Path)
	}
	if !json.Valid(doc) {
		return nil, nil, fmt.Errorf("running %s: artemis wrote something that is not JSON: %s", in.Path, doc)
	}
	return document(doc), json.RawMessage(doc), nil
}

// checkImports refuses a run when a scenario under target imports a file the
// workspace boundary would not let a tool read.
func (srv *server) checkImports(target string) error {
	var files []string
	err := filepath.WalkDir(target, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Dot-directories are walked too: being stricter than the CLI's
			// own walk costs nothing, and being looser would be a hole.
			return nil
		}
		if front.IsArtFile(d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range files {
		rel, err := filepath.Rel(srv.ws.root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // the target was resolved inside the workspace
		if err != nil {
			return err
		}
		for _, d := range front.CompileWith(rel, string(b), srv.ws.loader()).Bag.All() {
			if d.Code == diag.ImportNotFound {
				return fmt.Errorf("%s: %s", rel, d.Message)
			}
		}
	}
	return nil
}
