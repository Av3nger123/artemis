package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"artemis/pkg/shared/migrate"

	"github.com/spf13/cobra"
)

// outFlag is --out/-o: write the migrated source to this path instead of
// printing it.
const outFlag = "out"

var migrateCmd = &cobra.Command{
	Use:   "migrate -f <file.yaml> [-o <file.art>]",
	Short: "Convert a YAML scenario to a .art file, once",
	Long: `Read a YAML scenario and write the same scenario as Artemis DSL source.

	artemis migrate -f checkout.yaml              # to stdout
	artemis migrate -f checkout.yaml -o checkout.art

This is the one place artemis still reads YAML. The file is checked exactly as
'artemis parse' checks it -- a strict decode, and every step type validated
against the ones that can actually run -- so a scenario artemis would refuse to
run is refused here too and nothing is written.

The output goes through the same printer 'artemis fmt' uses, so a migrated file
is already canonically formatted. The YAML file is never touched, and never
deleted: nothing stops you running both for as long as you want to.

The comment above the file and the comment above each step are carried over.
Comments anywhere else are dropped, and how many is reported on stderr -- a
sentence written above one body check has no line in the migrated file to
belong to, because that check may become two expect lines or none.

A construct with no DSL spelling is an error naming the step it was in: a
wildcard, a recursive descent or a filter in a JSON path, 'operator: empty' on
a stream, an unclosed '{{'. Migration will not guess at one.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := cmd.Flags().GetString("file")
		if err != nil {
			return fmt.Errorf("reading --file flag: %w", err)
		}
		out, err := cmd.Flags().GetString(outFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", outFlag, err)
		}
		return migrateFile(cmd, path, out)
	},
}

// migrateFile is the whole command.
//
// It refuses a .art file, because migrating one is already done and reprinting
// it is what `artemis fmt` is for; saying so is better than writing a file
// somebody then has to diff to discover nothing happened.
func migrateFile(cmd *cobra.Command, path, out string) error {
	if isArtFile(path) {
		return fmt.Errorf("%s is already %s; artemis migrate converts a YAML scenario, and artemis fmt formats a %s file",
			path, artExt, artExt)
	}
	if out != "" && !isArtFile(out) {
		return fmt.Errorf("artemis migrate writes %s files; -o %s is not one", artExt, out)
	}

	// The same loader `artemis parse` uses, so the two agree on what a valid
	// YAML scenario is and migration cannot accept a file the runtime would
	// have rejected.
	config, err := loadScenario(path)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(path) //nolint:gosec // the path is the one the user named, and loadScenario has already read it
	if err != nil {
		return err
	}
	comments := migrate.ReadComments(raw)

	src, err := migrate.Source(config, comments)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", path, err)
	}
	reportDropped(cmd, path, comments.Dropped)

	if out == "" {
		_, err := fmt.Fprint(cmd.OutOrStdout(), src)
		return err
	}
	// writeFormatted is fmt -w's writer: beside and renamed over, with the
	// original's permission bits, so an interrupted migration leaves either the
	// old file or the whole new one.
	if err := ensureWritable(out); err != nil {
		return err
	}
	return writeFormatted(out, src)
}

// reportDropped says how many comments did not survive. On stderr, because
// stdout carries the migrated document and `artemis migrate -f x.yaml >
// x.art` has to be the file and nothing else.
func reportDropped(cmd *cobra.Command, path string, n int) {
	if n == 0 {
		return
	}
	line := "1 comment in %s had no place in the migrated file and was dropped\n"
	if n != 1 {
		line = "%[2]d comments in %[1]s had no place in the migrated file and were dropped\n"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), line, path, n)
}

// ensureWritable creates the file -o names if it is not there, so that
// writeFormatted -- which stats the file to carry its permission bits over --
// has something to stat. A migration is usually writing a file for the first
// time, which is the one case `fmt -w` never has.
func ensureWritable(out string) error {
	if _, err := os.Stat(out); err == nil {
		return nil
	}
	if dir := filepath.Dir(out); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the path is the one the user named
	if err != nil {
		return err
	}
	return f.Close()
}

// yamlExts is what a scenario file is called, for the error above. It is not a
// filter: `artemis migrate -f scenario.txt` is read as YAML, because the
// extension is a convention and the decoder is the authority.
var yamlExts = []string{".yaml", ".yml"}

// isYAMLFile reports whether path is named like a YAML file. Used by tests and
// by the corpus walk; migration itself does not insist on it.
func isYAMLFile(path string) bool {
	ext := filepath.Ext(path)
	for _, e := range yamlExts {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}
