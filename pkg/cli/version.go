package cli

import (
	"fmt"

	"artemis/pkg/version"

	"github.com/spf13/cobra"
)

// versionCmd is `artemis version`, the same line `artemis --version` prints,
// for whoever types the one they know from other tools.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Which artemis this is: the release, or the commit a source build came from",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), versionLine())
	},
}

func versionLine() string { return "artemis " + version.String() }
