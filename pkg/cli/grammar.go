package cli

import (
	"fmt"

	"artemis/pkg/dsl/grammar"

	"github.com/spf13/cobra"
)

var grammarCmd = &cobra.Command{
	Use:   "grammar [--json]",
	Short: "The language itself: the EBNF, or its enumerable choice points",
	Long: `Print the Artemis scenario language.

	artemis grammar          # the complete EBNF, and everything around it
	artemis grammar --json   # the enumerable choice points, for a form

The plain form is the whole language in one piece of text: the production
rules, the lexical tokens they treat as atoms, the precedence the productions
only imply, the rules the checker enforces after parsing, what each step type
binds, and one worked example that compiles. It is meant to be read once, or
pasted whole into a prompt -- a model that has never seen this syntax has seen
it after reading this.

The --json form is the other audience. It is every finite option set in the
language -- HTTP methods, browser actions with their arity, comparison
operators, type names, the fields of every block with the kind each one takes,
builtins, reserved words with what they are held for, step types with the
names they bind, and the diagnostic codes 'artemis parse --json' can emit. A
client building a form reads it instead of hardcoding a list that drifts:
every one of those is read out of the table the parser and the checker
themselves consult, so a word added to the language appears here without
anything shipping.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, err := cmd.Flags().GetBool(jsonFlag)
		if err != nil {
			return fmt.Errorf("reading --%s flag: %w", jsonFlag, err)
		}
		if asJSON {
			return grammar.WriteJSON(cmd.OutOrStdout())
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), grammar.Document())
		return err
	},
}
