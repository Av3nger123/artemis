package cli

import (
	"artemis/pkg/dsl/print"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// writeFlag is --write/-w: rewrite the file instead of printing it.
const writeFlag = "write"

var fmtCmd = &cobra.Command{
	Use:   "fmt [-w] <file.art>",
	Short: "Format a .art file in canonical layout",
	Long: `Format a .art file in canonical layout: two-space indentation, one space
either side of an operator, one item per line in a block that does not fit
inline, LF line endings, one trailing newline.

Formatting changes layout and nothing else. No string is requoted, no number
reformatted, no expression reassociated, and no comment dropped. Running it
twice changes nothing the second time.

Without -w the formatted file is printed to stdout and the file on disk is left
alone. A file artemis reports an error for is not formatted at all: the
diagnostics are printed and the file is left as it is.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return formatFile(cmd, args[0])
	},
}

// formatFile is the whole command. It refuses anything but a .art file: YAML
// has no canonical printer, and silently doing nothing to a file somebody asked
// to have formatted is worse than saying so.
func formatFile(cmd *cobra.Command, path string) error {
	if !isArtFile(path) {
		return fmt.Errorf("artemis fmt formats %s files; %s is not one", artExt, path)
	}

	// Errors first, before anything is printed or written. print.Canonical is
	// total -- it prints the source of a line that did not parse verbatim --
	// but idempotence is only claimed for a file that parses clean, and
	// rewriting a broken file is how a formatter loses someone's work.
	tree, _, err := loadArt(cmd, path)
	if err != nil {
		return err
	}

	formatted := print.Canonical(tree)
	write, _ := cmd.Flags().GetBool(writeFlag)
	if !write {
		_, err := fmt.Fprint(cmd.OutOrStdout(), formatted)
		return err
	}
	return writeFormatted(path, formatted)
}

// writeFormatted replaces path's contents with formatted, and is a no-op when
// they already match -- so `artemis fmt -w` over a suite that is already
// formatted does not touch a single mtime, and the second of two runs is free.
//
// The replacement is written beside the original and renamed over it, so a
// formatter that is interrupted leaves either the old file or the new one and
// never half of either. The original's permission bits are carried over: a file
// its owner kept to themselves must not become world-readable because it was
// formatted.
func writeFormatted(path, formatted string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if err != nil {
		return err
	}
	if string(current) == formatted {
		return nil
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".fmt")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// On any failure from here on the temp file is the thing to clean up; the
	// original is untouched until the rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(formatted); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
