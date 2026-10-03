package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// cobraTestCmd is a bare command for testing a flag reader without standing up
// the whole CLI. It exists because shotDir takes a *cobra.Command and the thing
// worth testing is what it does with the flag's value -- including the case of
// a command that does not have the flag at all.
type cobraTestCmd struct{ c *cobra.Command }

func (t *cobraTestCmd) cmd() *cobra.Command {
	if t.c == nil {
		t.c = &cobra.Command{Use: "t"}
	}
	return t.c
}

func (t *cobraTestCmd) flags() *pflag.FlagSet { return t.cmd().Flags() }
