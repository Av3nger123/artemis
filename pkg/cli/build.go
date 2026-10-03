package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/codegen"

	"github.com/spf13/cobra"
)

// langFlag is --lang: which target to generate. Required, because there is one
// implemented target today and three names the registry knows, and an invocation
// that means python now must not quietly mean something else later.
const langFlag = "lang"

var buildCmd = &cobra.Command{
	Use:   "build --lang=python [-o <dir>] <file.art>",
	Short: "Export a .art file as tests in another language",
	Long: `Export a scenario file as a test file for another runner.

	artemis build --lang=python checkout.art            # to stdout
	artemis build --lang=python -o tests/ checkout.art  # tests/test_checkout.py

The python target emits pytest: 'requests' for an api step, 'subprocess' for a
terminal step, and 'playwright.sync_api' for a browser step. One scenario becomes
one test function with its steps as ordered statements, a 'capture' becomes a
local variable, an 'expect' becomes a bare assert so pytest reports the operands,
a 'within' on a browser assertion becomes playwright's own timeout, and a 'retry'
becomes a loop around the step. 'pytest' runs the result with no artemis in the
picture.

The export is one way. Artemis never reads generated code back: the .art file
stays the source of truth, running the command again overwrites the output, and
the header of every generated file says both. It also lists the few places the
generated test and 'artemis run' differ, so neither has to be discovered.

The file is parsed and checked exactly as 'artemis parse' checks it. A file with
an error is reported and nothing is written.

Without -o the generated source goes to stdout. With -o it names a directory,
created if it is not there, and the path of each file written is printed. There
is no --force: the output is derived, and regenerating it is the point.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		lang, err := cmd.Flags().GetString(langFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", langFlag, err)
		}
		out, err := cmd.Flags().GetString(outFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", outFlag, err)
		}
		return buildFile(cmd, args[0], lang, out)
	},
}

// buildFile is the whole command.
//
// The order matters: the language is resolved before the file is read, so
// `artemis build --lang=go x.art` answers about the target rather than spending a
// parse first, and the file is checked before anything is written, so a scenario
// that does not compile leaves no half-exported test behind.
func buildFile(cmd *cobra.Command, path, lang, out string) error {
	if !isArtFile(path) {
		return fmt.Errorf("artemis build exports %s files; %s is not one", artExt, path)
	}
	target, err := codegen.Lookup(lang)
	if err != nil {
		return err
	}

	tree, _, err := loadArt(cmd, path)
	if err != nil {
		return err
	}

	files, err := target.Generate(tree)
	if err != nil {
		return fmt.Errorf("build %s for %s: %w", path, target.Name(), err)
	}
	if len(files) == 0 {
		return fmt.Errorf("the %s target produced nothing for %s", target.Name(), path)
	}

	if out == "" {
		if len(files) > 1 {
			return fmt.Errorf("the %s target produces %d files for %s; name a directory with -%s",
				target.Name(), len(files), path, "o")
		}
		_, err := fmt.Fprint(cmd.OutOrStdout(), files[0].Content)
		return err
	}
	return writeGenerated(cmd, out, files)
}

// writeGenerated writes every generated file under dir and prints each path.
//
// The paths are printed because that is the only output the command has in this
// mode, and a command that writes files silently is one you have to go and look
// for. They go to stdout: they are the document this invocation produced.
func writeGenerated(cmd *cobra.Command, dir string, files []codegen.GeneratedFile) error {
	for _, f := range files {
		// A target names a path relative to the output directory. Cleaning it and
		// refusing one that climbs out is cheap, and the alternative is a target
		// bug that writes outside the directory the user named.
		rel := filepath.Clean(filepath.FromSlash(f.Path))
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("the target asked to write %s, which is outside %s", f.Path, dir)
		}
		full := filepath.Join(dir, rel)
		if parent := filepath.Dir(full); parent != "" {
			if err := os.MkdirAll(parent, 0o750); err != nil {
				return err
			}
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), full)
	}
	return nil
}
