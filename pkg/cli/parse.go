package cli

import (
	"fmt"

	"artemis/pkg/dsl/diag"

	"github.com/spf13/cobra"
)

// jsonFlag is --json: diagnostics as a document rather than as a report.
const jsonFlag = "json"

var parseCmd = &cobra.Command{
	Use:   "parse",
	Short: "Check a scenario file without running any of it",
	Long: `Check the .art file named by --file and run nothing: no request is
sent, no command is run.

The file is lexed, parsed and name-checked, and every problem artemis finds is
reported -- all of them, in file order, with the source line echoed. Any error
exits non-zero.

With --json the same diagnostics go to stdout as one document instead:

	{"diagnostics": [
	  {"code": "unknown-field", "severity": "error",
	   "span": {"file": "x.art", "line": 12, "col": 10, "endLine": 12, "endCol": 15, "offset": 203},
	   "message": "unknown field \"statu\"",
	   "hint": "did you mean \"status\"?",
	   "suggestions": [{"replace": "status"}]}
	]}

This is what an editor or the UI builder reads. The code is stable and the
message is not, so key behaviour off the code; the span's offset and endCol
locate the text to underline; a suggestion's replace is a mechanical fix, so a
client can offer one-click correction. A clean file writes {"diagnostics": []}.
The exit status is the same either way: non-zero when the file does not
compile, with the count on stderr. It is a .art flag: a YAML file has no
pkg/dsl/diag diagnostics to emit.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, err := cmd.Flags().GetString("file")
		if err != nil {
			return fmt.Errorf("reading --file flag: %w", err)
		}
		asJSON, err := cmd.Flags().GetBool(jsonFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", jsonFlag, err)
		}
		if asJSON {
			if !isArtFile(filePath) {
				return fmt.Errorf("--%s describes %s diagnostics; %s is not one", jsonFlag, artExt, filePath)
			}
			return parseJSON(cmd, filePath)
		}
		// One format, one command. A path that is not .art is refused with
		// the way out spelled for it, rather than handed to the lexer -- a
		// YAML file run through the .art parser is a page of diagnostics
		// about a file that was never meant to be one.
		if !isArtFile(filePath) {
			return notAScenarioError(filePath)
		}
		if _, _, err := loadArt(cmd, filePath); err != nil {
			return err
		}
		// A command whose only job is to tell you something has to say
		// something when the answer is yes.
		fmt.Fprintf(cmd.OutOrStdout(), "%s: ok\n", filePath)
		return nil
	},
}

// parseJSON is `artemis parse -f x.art --json`.
//
// The document is written whether or not the file compiles, because a client
// asking what is wrong with a file is asking precisely when something is. The
// non-zero exit is kept: --json is a way to read the diagnostics, not a way to
// make a broken file pass.
func parseJSON(cmd *cobra.Command, path string) error {
	f, err := readArt(path)
	if err != nil {
		return err
	}
	if err := diag.JSON(cmd.OutOrStdout(), f.diags); err != nil {
		return fmt.Errorf("writing the diagnostics of %s: %w", path, err)
	}
	return f.err()
}
