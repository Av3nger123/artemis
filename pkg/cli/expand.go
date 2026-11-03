package cli

import (
	"fmt"
	"io"
	"os"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/diag"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"

	"github.com/spf13/cobra"
)

var expandCmd = &cobra.Command{
	Use:   "expand <file.art> [-o <out.art>] [--force]",
	Short: "Print what a scenario file runs: each scenario as its flow, every item call as its step",
	Long: `Read a scenario file, resolve its flow and imports from disk, and print what
will actually run: each scenario as its flow with the scenario's values in its
vars, no import, no collection and no item call, only steps.

	artemis expand scenarios/checkout.art                      # to stdout
	artemis expand scenarios/checkout.art -o checkout.flat.art

Each step carries a comment saying where it came from: the item or the flow,
the line it is declared on, and the line that brought it in.

The output is for reading. It is written as a scenario that holds its steps,
which is a form artemis no longer compiles, so it is not a file to run.

The file is checked first, exactly as 'artemis run' checks it, and nothing is
printed or written for a file with errors. A flow file is refused, naming the
scenario files that run it, and so is a file with no scenario in it. Without
-o the result goes to stdout. With -o, a file that is already there is an
error rather than an overwrite; --force replaces it.`,
	Args: cobra.ExactArgs(1),
	RunE: runExpand,
}

func runExpand(cmd *cobra.Command, args []string) error {
	path := args[0]
	if !isArtFile(path) {
		return fmt.Errorf("artemis expand reads %s files; %s is not one", artExt, path)
	}
	out, err := cmd.Flags().GetString(outFlag)
	if err != nil {
		return fmt.Errorf("reading --%s flag: %w", outFlag, err)
	}
	force, err := cmd.Flags().GetBool(forceFlag)
	if err != nil {
		return fmt.Errorf("reading --%s flag: %w", forceFlag, err)
	}
	// Refused before anything is read, as generate does: the clobber guard
	// is the reason -o can be trusted.
	if out != "" {
		if !isArtFile(out) {
			return fmt.Errorf("artemis expand writes %s files; -o %s is not one", artExt, out)
		}
		if _, err := os.Stat(out); err == nil && !force {
			return fmt.Errorf("%s already exists; pass --%s to replace it, or name another file with -o", out, forceFlag)
		}
	}

	if isFlow, err := flowNotExpandable(path, cmd.ErrOrStderr()); isFlow {
		return err
	}

	f, err := readArt(path)
	if err != nil {
		return err
	}
	if err := f.render(cmd.ErrOrStderr()); err != nil {
		return err
	}
	if err := f.err(); err != nil {
		return err
	}
	if !hasScenario(f.unit.Expanded) {
		return fmt.Errorf("%s holds no scenario to expand; artemis expand reads a scenario file, whose first line is flow \"<path to the flow>\"", path)
	}

	text := print.Canonical(withOrigins(f.unit))
	if out == "" {
		_, err := fmt.Fprint(cmd.OutOrStdout(), text)
		return err
	}
	if err := ensureWritable(out); err != nil {
		return err
	}
	return writeFormatted(out, text)
}

// flowNotExpandable reports path when it is a flow file, as run does: a flow
// has no values of its own to expand with, so the diagnostic names the
// scenario files that run it. isFlow is false for any other file, and for one
// that cannot be read, which readArt reports.
func flowNotExpandable(path string, diagOut io.Writer) (isFlow bool, err error) {
	src, readErr := os.ReadFile(path) //nolint:gosec // the path is the one the user named
	if readErr != nil {
		return false, nil
	}
	d, isFlow := flowFileDiag(path, string(src), ".")
	if !isFlow {
		return false, nil
	}
	d.Message = fmt.Sprintf("%s is a flow; expand a scenario file that names it", path)
	files := diag.NewFiles()
	files.Add(path, string(src))
	if err := diag.Terminal(diagOut, files, []diag.Diagnostic{d}); err != nil {
		return true, err
	}
	return true, firstError([]diag.Diagnostic{d})
}

// hasScenario reports whether tree holds a scenario: a collection file
// compiles clean and expands to nothing a reader could want printed.
func hasScenario(tree *ast.File) bool {
	for _, d := range tree.Scenarios {
		if _, ok := d.(*ast.Scenario); ok {
			return true
		}
	}
	return false
}

// withOrigins is the expanded tree laid out for a reader, with a comment above
// every step a use brought in:
//
//	# from auth.login (auth.art:2) via checkout.art:6
//
// The step's name token carries the Via of the innermost use that copied it
// in, so that use's item is what `from` names; `via` names the scenario's own
// use line, at the root of the chain. A step written in the scenario itself
// has Via 0 and gets nothing.
//
// Trivia is replaced with a new slice and never appended to: ast.Clone shares
// trivia with the collection's tree, and an append into its spare capacity
// would write a comment into a file nobody asked to change.
func withOrigins(u *front.Unit) *ast.File {
	for _, d := range u.Expanded.Scenarios {
		sc, ok := d.(*ast.Scenario)
		if !ok {
			continue
		}
		for _, b := range sc.Body {
			if s, ok := b.(*ast.StepDecl); ok {
				if text, ok := origin(u, s.Name.Span.Via); ok {
					s.Keyword.Leading = withComment(s.Keyword.Leading, text)
				}
			}
		}
	}
	return u.Expanded
}

// origin is the comment for a step copied in by use number via, and false for
// a step written where it stands.
func origin(u *front.Unit, via int) (string, bool) {
	if via <= 0 || via > len(u.Uses) {
		return "", false
	}
	use := u.Uses[via-1]
	return fmt.Sprintf("# from %s (%s:%d) via %s:%d",
		use.Ref, use.Item.File, use.Item.Line, use.Span.File, use.Span.Line), true
}

// withComment is leading with an own-line comment after it, in a new slice.
func withComment(leading []token.Trivia, text string) []token.Trivia {
	out := make([]token.Trivia, 0, len(leading)+2)
	out = append(out, leading...)
	return append(out,
		token.Trivia{Kind: token.Comment, Text: text},
		token.Trivia{Kind: token.Newline, Text: "\n"})
}
