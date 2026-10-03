package cli

import (
	"fmt"
	"io"
	"os"

	"artemis/pkg/dsl/encode"
	"artemis/pkg/dsl/print"

	"github.com/spf13/cobra"
)

// fromJSONFlag is --from-json: read a tree instead of writing one.
const fromJSONFlag = "from-json"

var astCmd = &cobra.Command{
	Use:   "ast (-f <file.art> | --from-json [-f <tree.json>])",
	Short: "The syntax tree as JSON, both directions",
	Long: `Write a .art file's syntax tree as JSON, or turn such a tree back into
source.

	artemis ast -f checkout.art             # tree as JSON
	artemis ast --from-json < tree.json     # JSON back to source

This is the contract a UI, a transpiler and the interpreter all read, so none of
them can drift: it is the same tree, from the same front end that runs the
suite. Every node carries its span -- file, line, col, endLine, endCol, offset --
and the document carries a schemaVersion, because a UI ships and upgrades
independently of the CLI.

The checker's conclusions travel with the nodes they are about. Each step has
the type its action implies and the list of names resolvable inside it, and each
expect has the simple/complex label that decides whether a form renders three
widgets or one raw expression field. Nothing re-derives them.

A file artemis reports an error for produces no JSON: the diagnostics are
printed and the exit status is non-zero. A tree holding a node that did not
parse is described faithfully on the way out, and refused on the way in -- a
complete tree is what artemis reads.

--from-json is a conversion and nothing else. It writes canonical source and
exits zero whenever the document decodes; 'artemis parse' is the command that
reports what is wrong with a file.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := cmd.Flags().GetString("file")
		if err != nil {
			return fmt.Errorf("reading --file flag: %w", err)
		}
		fromJSON, err := cmd.Flags().GetBool(fromJSONFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", fromJSONFlag, err)
		}
		if fromJSON {
			return astFromJSON(cmd, path)
		}
		return astToJSON(cmd, path)
	},
}

// astToJSON is `artemis ast -f x.art`.
//
// The front end runs in full, because the document carries the checker's labels
// and there is no honest way to write them without it. A file with an error
// produces no document at all: a UI that loaded a half-described tree and wrote
// it back would lose the parts artemis could not describe, so the diagnostics
// are the answer instead.
func astToJSON(cmd *cobra.Command, path string) error {
	if path == "" {
		return fmt.Errorf("artemis ast needs a file: -f <file.art>, or --from-json to read a tree")
	}
	if !isArtFile(path) {
		return fmt.Errorf("artemis ast reads %s files; %s is not one", artExt, path)
	}

	tree, info, err := loadArt(cmd, path)
	if err != nil {
		return err
	}
	doc, err := encode.Encode(tree, info)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	_, err = cmd.OutOrStdout().Write(doc)
	return err
}

// astFromJSON is `artemis ast --from-json`.
//
// The document comes from stdin, which is the form the design document shows
// and the one a UI uses, or from -f when somebody is holding a file and wants to
// look at it.
func astFromJSON(cmd *cobra.Command, path string) error {
	data, err := readTree(cmd, path)
	if err != nil {
		return err
	}
	tree, err := encode.Decode(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), print.Canonical(tree))
	return err
}

// readTree is the document, from the named file or from stdin.
//
// An empty stdin is an error rather than an empty file, because it is almost
// always a pipe that produced nothing -- `artemis ast -f missing.art | artemis
// ast --from-json` -- and printing nothing in reply to that would hide the
// first command's failure.
func readTree(cmd *cobra.Command, path string) ([]byte, error) {
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil, fmt.Errorf("reading the tree from stdin: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("nothing on stdin: artemis ast --from-json reads a tree there, or from -f <tree.json>")
	}
	return data, nil
}
