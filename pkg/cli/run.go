package cli

import (
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <path>",
	Short: "Run the scenarios in a file or a folder",
	Long: `Run the scenarios artemis finds at <path>.

A file is run on its own -- a .art file, or a YAML one. A folder is walked
recursively for *.art, *.yaml and *.yml files, which are run in path order as
one run: one summary, one exit code, whichever formats it holds.

A .art file is lexed, parsed, name-checked and lowered before anything is sent.
Every problem artemis finds is reported with the source line echoed, and a file
that does not compile is one errored scenario: the files after it still run.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		closer, err := initRunEnv(cmd)
		if err != nil {
			return err
		}
		defer closer.Close()
		return reportRun(cmd, args[0])
	},
}

// initRunEnv opens the log file and loads the env file for a run, and returns
// what has to be closed when it ends. Both `run` and the deprecated `test` come
// through here, so they cannot drift apart.
func initRunEnv(cmd *cobra.Command) (io.Closer, error) {
	logFilePath, _ := cmd.Flags().GetString("log")
	envFilePath, _ := cmd.Flags().GetString("env")
	closer, err := logger.InitLog(logFilePath)
	if err != nil {
		return nil, err
	}
	if err := env.InitEnv(envFilePath); err != nil {
		logger.Logger.Warn("Could not load env file", "path", envFilePath, "error", err.Error())
		// A missing .env is the normal case and not worth a line; a path the
		// user named and that did not load is.
		if cmd.Flags().Changed("env") {
			// err already names the path.
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
		}
	}
	return closer, nil
}

// reportRun discovers the scenarios at path, runs them as one run, prints the
// summary, writes whatever --report asked for, and returns an error if anything
// failed -- that error is what makes the process exit non-zero.
func reportRun(cmd *cobra.Command, path string) error {
	// Before discovery: a mistyped format must not cost a suite run.
	reportValues, _ := cmd.Flags().GetStringArray(reportFlag)
	targets, err := parseReports(reportValues)
	if err != nil {
		return err
	}

	files, err := discover(path)
	if err != nil {
		return err
	}

	// cmd.OutOrStdout, not os.Stdout: a test captures the run's output by
	// setting the command's writer.
	//
	// When a report goes to stdout, the console goes to stderr instead, so
	// stdout is exactly one document and `artemis run suite --report json | jq`
	// works. It is moved rather than silenced: a person watching a long suite
	// should still see steps tick past, and the console is the only thing that
	// prints the failure breakdown.
	out := cmd.OutOrStdout()
	consoleOut := out
	if anyToStdout(targets) {
		consoleOut = cmd.ErrOrStderr()
	}

	rep := report.NewConsole(consoleOut)
	// Diagnostics go to stderr whatever the console does: a .art file that will
	// not compile is reported with its caret gutter, and stdout stays exactly
	// one document when --report asked for one.
	run := runFiles(executor.Default(), files, rep, cmd.ErrOrStderr())
	// Between the step lines and the tallies: the blocks are what a reader --
	// or the agent that wrote the scenario -- acts on (ART-12), and the
	// summary's verdict stays the last line a run writes.
	rep.Failures(run)
	rep.Summary(run)

	// A report that could not be written wins over the run's own failure: the
	// console has already said the run failed, and the missing artifact is the
	// part the caller does not know about yet.
	if err := writeReports(out, targets, run); err != nil {
		return err
	}
	if !run.Passed() {
		return runFailedError(run)
	}
	return nil
}

// scenarioExts are the extensions a folder walk picks up. A file named outright
// is run whatever it is called -- the user said which file they meant.
//
// `.art` is here alongside the two YAML spellings rather than instead of them,
// so a suite can be migrated a file at a time: a folder holding both runs both,
// in one path order, as one run with one summary and one exit code. The two
// formats leave together, when ART-40 takes YAML off the run path.
var scenarioExts = []string{".yaml", ".yml", ".art"}

// discover turns the path a user named into the list of scenario files to run.
//
// A folder is walked recursively. filepath.WalkDir visits lexically, so the
// order is the same on every machine and every run, which is what makes a
// folder run's transcript comparable with the last one's. Directories whose
// name starts with a dot are skipped: .git and .github hold no scenarios, and
// walking them is a surprise. Symlinked directories are not followed, so a
// suite that links to itself cannot hang the run.
//
// A path that matches nothing is an error. A suite that quietly shrank to
// nothing must not report a passing run.
func discover(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}

	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The root is walked whatever it is called: a user who asks for
			// ./.suite means it.
			if p != path && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if isScenarioFile(d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", path, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no scenario files (%s) found in %s", strings.Join(scenarioExts, ", "), path)
	}
	return files, nil
}

func isScenarioFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, want := range scenarioExts {
		if ext == want {
			return true
		}
	}
	return false
}

// runFiles runs every file as a scenario of one run. It never returns nil: a
// run with nothing in it is a run that passed.
//
// A file artemis cannot load is recorded as an errored scenario and the rest
// still run. Stopping at the first bad file would hide every other result of
// the suite behind one typo. That holds for either format: a .art file with a
// diagnostic is a file artemis cannot load.
//
// The dispatch is on the extension, which is the one place the run path knows
// there are two formats. It goes with the YAML reader (ART-40).
func runFiles(reg *executor.Registry, files []string, rep *report.Console, diagOut io.Writer) *result.RunResult {
	run := result.NewRun()
	for _, file := range files {
		if isArtFile(file) {
			runArtFile(context.Background(), reg, file, run, rep, diagOut)
			continue
		}
		config, err := loadScenario(file)
		if err != nil {
			// No name to give it: the file would not parse, so all anyone
			// knows about this scenario is where it lives.
			scenario := run.NewScenario("", file)
			rep.Scenario(scenario)
			scenario.Fail(0, err)
			rep.ScenarioFailed(scenario)
			logger.Logger.Error("Could not load a scenario", "file", file, "error", err.Error())
			continue
		}
		executeScenario(context.Background(), reg, config, file, run, rep)
	}
	run.Finish()
	return run
}

// runFailedError states in one line what failed. The readable breakdown is the
// console summary on stdout; this is the reason attached to a non-zero exit, and
// it is what survives when only stderr is kept.
func runFailedError(run *result.RunResult) error {
	c := run.Counts()
	var parts []string
	// Only the levels that actually failed get a clause. A run that failed
	// solely because a file would not load must not open with "0 of 2 steps
	// failed" -- two steps ran and both passed.
	if bad := c.Steps.Failed + c.Steps.Errored; bad > 0 {
		msg := fmt.Sprintf("%d of %d steps failed", bad, c.Steps.Total)
		if c.Steps.Errored > 0 {
			msg += fmt.Sprintf(" (%d errored)", c.Steps.Errored)
		}
		if badAsserts := c.Assertions.Failed + c.Assertions.Errored; badAsserts > 0 {
			msg += fmt.Sprintf(", %d of %d assertions failed", badAsserts, c.Assertions.Total)
		}
		parts = append(parts, msg)
	}
	// A scenario that failed with no steps at all never ran: its file would not
	// load. Counting its steps says "0 of 0 steps failed", so it gets its own
	// clause, and it carries the reasons: this error is what survives when only
	// stderr is kept, and a file that would not load is not actionable without
	// its path and the reason.
	if reasons := unrunnable(run); len(reasons) > 0 {
		parts = append(parts, fmt.Sprintf("%s could not run: %s",
			plural(len(reasons), "scenario", "scenarios"), strings.Join(reasons, "; ")))
	}
	if len(parts) == 0 {
		return errors.New("the run failed")
	}
	return errors.New(strings.Join(parts, "; "))
}

// unrunnable returns the reason for every scenario that did not pass and got no
// steps -- a file artemis could not load. Each reason already names the file.
func unrunnable(run *result.RunResult) []string {
	var reasons []string
	for _, sc := range run.Scenarios {
		if !sc.Passed() && len(sc.Steps) == 0 {
			reason := sc.Error
			if reason == "" {
				reason = sc.File
			}
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
