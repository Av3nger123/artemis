package cli

import (
	"fmt"
	"os"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/front"
	"artemis/pkg/dsl/print"
	"artemis/pkg/dsl/token"

	"github.com/spf13/cobra"
)

var expandCmd = &cobra.Command{
	Use:   "expand <file.art> [-o <out.art>] [--force]",
	Short: "Print a scenario file with every use replaced by the steps it stands for",
	Long: `Read a scenario file, resolve its imports from disk, and print the scenario
that will actually run: no import, no collection and no use, only steps.

	artemis expand checkout.art                    # to stdout
	artemis expand checkout.art -o checkout.flat.art

Each step a use brought in carries a comment saying where it came from: the
request or flow, the line it is declared on, and the use line that brought it
in. A secret parameter's argument is a 'secret var' just above the steps that
read it.

The file is checked first, exactly as 'artemis run' checks it, and nothing is
printed or written for a file with errors. Without -o the scenario goes to
stdout. With -o, a file that is already there is an error rather than an
overwrite; --force replaces it.`,
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
// A run of hoisted secret vars that follows a step gets a blank line above
// it, so it reads as part of the use below it and not of the step above.
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
		for i, b := range sc.Body {
			switch s := b.(type) {
			case *ast.StepDecl:
				if text, ok := origin(u, s.Name.Span.Via); ok {
					s.Keyword.Leading = withComment(s.Keyword.Leading, text)
				}
			case *ast.VarDecl:
				if i == 0 || s.Name.Span.Via == 0 || s.Secret.Text == "" {
					continue
				}
				if _, afterStep := sc.Body[i-1].(*ast.StepDecl); afterStep {
					s.Secret.Leading = []token.Trivia{
						{Kind: token.Newline, Text: "\n"},
						{Kind: token.Newline, Text: "\n"},
					}
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
	// The use the scenario wrote: walk out through every use that copied this
	// one in, so `via` is a line of the scenario file a reader can go to.
	top := use
	for top.Parent > 0 && top.Parent <= len(u.Uses) {
		top = u.Uses[top.Parent-1]
	}
	return fmt.Sprintf("# from %s (%s:%d) via %s:%d",
		use.Ref, use.Item.File, use.Item.Line, top.Span.File, top.Span.Line), true
}

// withComment is leading with an own-line comment after it, in a new slice.
func withComment(leading []token.Trivia, text string) []token.Trivia {
	out := make([]token.Trivia, 0, len(leading)+2)
	out = append(out, leading...)
	return append(out,
		token.Trivia{Kind: token.Comment, Text: text},
		token.Trivia{Kind: token.Newline, Text: "\n"})
}
