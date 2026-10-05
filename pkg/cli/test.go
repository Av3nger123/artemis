package cli

import (
	"fmt"
	"time"

	// Registers the step types. The runner reaches them through the registry
	// and never names them -- bar the browser, whose assertions read a live
	// page the registry cannot hand over; this is the one place the binary
	// says which types it is built with.
	_ "artemis/pkg/steps/browserstep"
	_ "artemis/pkg/steps/execstep"
	_ "artemis/pkg/steps/httpstep"

	"github.com/spf13/cobra"
)

// testCmd is what `artemis run` replaces: the same run with the path read off a
// flag instead of the command line. It is kept so that every README, script and
// CI job written against the only runner artemis has ever had keeps working, and
// cobra tells its users where to go. Delete it a release after `run` has
// shipped -- ART-40 deliberately did not, because retiring a command is a
// user-visible break and that issue's point was that nothing a scenario can say
// changes.
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run the scenarios in one file (deprecated: use `artemis run`)",
	Long: "Run the scenarios in the .art file named by --file.\n\n" +
		"Deprecated: use `artemis run <path>`, which also takes a folder.",
	Deprecated: "use `artemis run <path>` instead; it takes a folder as well as a file.",
	RunE: func(cmd *cobra.Command, args []string) error {
		re, err := initRunEnv(cmd)
		if err != nil {
			return err
		}
		defer re.closer.Close()
		return runTest(cmd, re.envFile)
	},
}

// runTest runs the one file named by --file. It is `artemis run <file>` with the
// path read off a flag, and goes when the deprecated `test` command goes.
func runTest(cmd *cobra.Command, envFile string) error {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("reading --file flag: %w", err)
	}
	return reportRun(cmd, filePath, envFile)
}

// sleep is how the retry loop waits between attempts and how a waiting
// assertion waits between tries. It is a variable so a test can record the
// delays asked for instead of waiting them out.
var sleep = time.Sleep

// now is the clock both loops read. It is a variable for the same reason sleep
// is, and the two go together: a test that fakes only one of them either waits
// out a real budget or spins against a clock that never moves. Faking both lets
// the settle loop be tested on a virtual clock, in microseconds, with the
// number of tries it made being the thing asserted.
var now = time.Now
