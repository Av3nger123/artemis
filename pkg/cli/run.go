package cli

import (
	"artemis/pkg/executor"
	"artemis/pkg/report"
	"artemis/pkg/result"
	"artemis/pkg/session"
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/steps/browserstep"
	"bytes"
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

A file is run on its own. A folder is walked recursively for *.art files,
which are run in path order as one run: one summary, one exit code.

A .art file is lexed, parsed, name-checked and lowered before anything is sent.
Every problem artemis finds is reported with the source line echoed, and a file
that does not compile is one errored scenario: the files after it still run.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		re, err := initRunEnv(cmd)
		if err != nil {
			return err
		}
		defer re.closer.Close()
		return reportRun(cmd, args[0], re.envFile)
	},
}

// runEnv is what a run has set up before it starts: what to close when it ends,
// and the env file that actually loaded.
//
// envFile is the path only when godotenv read it, and empty otherwise -- an
// absent default .env is the normal case, and a `-e prod.env` that could not be
// read has already been warned about. The environment gate's block names the
// file it read, so a run in a directory with no .env must not say "read .env":
// that sends the reader to inspect a file that is not there instead of at the
// variable they have to set.
type runEnv struct {
	closer  io.Closer
	envFile string
}

// initRunEnv opens the log file and loads the env file for a run, and returns
// what has to be closed when it ends. Both `run` and the deprecated `test` come
// through here, so they cannot drift apart.
func initRunEnv(cmd *cobra.Command) (runEnv, error) {
	logFilePath, _ := cmd.Flags().GetString("log")
	envFilePath, _ := cmd.Flags().GetString("env")
	closer, err := logger.InitLog(logFilePath)
	if err != nil {
		return runEnv{}, err
	}
	loaded := envFilePath
	if err := env.InitEnv(envFilePath); err != nil {
		// Nothing was read, so nothing is reported as read.
		loaded = ""
		logger.Logger.Warn("Could not load env file", "path", envFilePath, "error", err.Error())
		// A missing .env is the normal case and not worth a line; a path the
		// user named and that did not load is.
		if cmd.Flags().Changed("env") {
			// err already names the path.
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
		}
	}
	return runEnv{closer: closer, envFile: loaded}, nil
}

// reportRun discovers the scenarios at path, runs them as one run, prints the
// summary, writes whatever --report asked for, and returns an error if anything
// failed -- that error is what makes the process exit non-zero.
//
// envFile is the env file the run read, or empty when it read none; it is only
// here because the environment gate's block names it.
func reportRun(cmd *cobra.Command, path, envFile string) error {
	// Before discovery: a mistyped format must not cost a suite run.
	reportValues, _ := cmd.Flags().GetStringArray(reportFlag)
	targets, err := parseReports(reportValues)
	if err != nil {
		return err
	}
	// The deprecated `test` has no --events, and GetString answers "" for a
	// flag the command does not have, which is off.
	eventsValue, _ := cmd.Flags().GetString(eventsFlag)
	streaming, err := parseEvents(eventsValue, targets)
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
	// When a report or the event stream goes to stdout, the console goes to
	// stderr instead, so stdout is exactly one document -- or exactly one
	// event per line -- and `artemis run suite --report json | jq` works. It is
	// moved rather than silenced: a person watching a long suite should still
	// see steps tick past, and the console is the only thing that prints the
	// failure breakdown.
	out := cmd.OutOrStdout()
	consoleOut := out
	if anyToStdout(targets) || streaming {
		consoleOut = cmd.ErrOrStderr()
	}

	rep := report.NewConsole(consoleOut)
	// Diagnostics go to stderr whatever the console does: a .art file that will
	// not compile is reported with its caret gutter, and stdout stays exactly
	// one document when --report asked for one.
	rt := &runtimeEnv{
		reg:   executor.Default(),
		shots: browserstep.NewShots(shotDir(cmd)),
	}
	if streaming {
		rt.events = report.NewEvents(out)
	}
	run := runFilesWith(rt, files, rep, cmd.ErrOrStderr())

	// Before the failure blocks and the summary, which is where a compile
	// diagnostic goes too (SPEC.md): the environment error copies the compile
	// error's habits, and a gated run's summary is an all-zeros `FAIL in <1ms`
	// that explains nothing on its own. Printed after it, the block arrives
	// once the reader has already gone looking for a cause.
	//
	// It goes to stderr beside the diagnostics, not to the console writer:
	// stdout stays exactly one document when --report asked for one.
	if len(rt.envFaults) > 0 {
		envGateReport(cmd.ErrOrStderr(), rt.envFaults, envFile)
	}

	// Between the step lines and the tallies: the blocks are what a reader --
	// or the agent that wrote the scenario -- acts on (ART-12), and the
	// summary's verdict stays the last line the *run* writes.
	rep.Failures(run)
	rep.Summary(run)

	// A report that could not be written wins over the run's own failure: the
	// console has already said the run failed, and the missing report is the
	// part the caller does not know about yet.
	if err := writeReports(out, targets, run); err != nil {
		return err
	}
	// Not for the exit code -- runFilesWith already forced Status to
	// StatusError, so !run.Passed() is true and the process exits non-zero
	// either way. This is for the message: a gated run holds no failed
	// scenario and no failed step, so runFailedError finds no clause to write
	// and falls back to "the run failed", which names nothing. run.Error
	// already holds the text runFilesWith built from the same faults, so this
	// reuses it rather than building it a second time from rt.envFaults.
	if len(rt.envFaults) > 0 {
		return errors.New(run.Error)
	}
	if !run.Passed() {
		return runFailedError(run)
	}
	return nil
}

// scenarioExts are the extensions a folder walk picks up. A file named outright
// is opened whatever it is called -- the user said which file they meant, and
// runFiles is what tells them it is not a scenario artemis can run.
//
// It was `.yaml`, `.yml`, `.art` while a suite was being migrated a file at a
// time. ART-40 took YAML off the run path, so a folder that still holds
// unconverted YAML runs the converted half and says nothing about the rest --
// which is the right silence: the YAML files were never going to be found by a
// walk again, and `artemis migrate` is how they come back.
var scenarioExts = []string{".art"}

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
// the suite behind one typo. A .art file with a diagnostic is such a file, and
// so is a file that is not .art at all.
//
// Only a path the user named outright can reach that second branch, because the
// folder walk picks up nothing else. It is an errored scenario rather than a
// skip: someone who types `artemis run login.yaml` and gets a passing run that
// ran nothing has been told the opposite of the truth.
func runFiles(reg *executor.Registry, files []string, rep *report.Console, diagOut io.Writer) *result.RunResult {
	return runFilesWith(&runtimeEnv{reg: reg}, files, rep, diagOut)
}

// runtimeEnv is what the runner needs besides the scenarios: the executors to
// dispatch to, and where a failed browser step's screenshot goes.
//
// It is one value threaded down to runArtStep rather than two more parameters
// on four functions, and it is where the next run-wide setting goes. A nil
// shots is screenshots off, which is what every test that is not about them
// gets.
type runtimeEnv struct {
	reg   *executor.Registry
	shots *browserstep.Shots
	// events is the --events stream, nil when it was not asked for. Every
	// method on a nil *report.Events writes nothing, so the runner calls them
	// unconditionally rather than asking first.
	events *report.Events
	// envFaults is what the check found, for reportRun to print.
	//
	// There is no lookup seam beside it: envGate takes the lookup as a
	// parameter and that is what its own unit tests drive, so a field here
	// would have had no caller but itself.
	envFaults []envFault
}

// runFilesWith is runFiles with the run-wide settings given rather than
// defaulted. `artemis run` goes through here; runFiles is the plain form, which
// is what the tests that are not about screenshots use.
//
// session.Shutdown is deferred once for the whole run rather than per scenario.
// The Playwright driver -- a Node process -- is per process; starting one per
// scenario would cost an install check and a pipe each time. The *browser* is
// what scenarios must not share, and that is the registry's business, closed at
// each scenario boundary. Shutdown is a no-op when nothing ever opened a
// session, which is the normal case and the reason it can be deferred
// unconditionally: an api-only run does not have to know it had no browser.
func runFilesWith(rt *runtimeEnv, files []string, rep *report.Console, diagOut io.Writer) *result.RunResult {
	defer func() {
		if err := session.Shutdown(); err != nil {
			logger.Logger.Warn("Could not stop the browser driver", "error", err.Error())
		}
	}()

	run := result.NewRun()
	// Before anything compiles: a watcher learns the shape of the run first,
	// including the files that will turn out not to load.
	rt.events.RunStart(files)

	// Every file is compiled before any file runs, which is what the check
	// before the run needs: an absent variable has to be reported before the
	// first step touches the system under test. The trees stay in memory
	// rather than being compiled twice, because compileArt renders its
	// diagnostics as it goes and a second pass would print all of them again.
	//
	// Nothing is appended to run, and nothing is written to diagOut, during
	// this pass. A file's place in run.Scenarios -- and its diagnostics'
	// place in diagOut, right before that scenario's own report, as they have
	// always been -- has to match the order files were discovered in. This
	// pass learns the outcome of every file before it learns which file, if
	// any, the environment gate stops; recording outcomes as they are found
	// here would put every file that would not compile ahead of every file
	// that ran, which is an ordering no one asked for. Each file's
	// diagnostics are held in its own buffer, which is what lets them be
	// printed in that order out of the single compile above.
	attempts := make([]fileAttempt, len(files))
	for i, file := range files {
		if !isArtFile(file) {
			attempts[i] = fileAttempt{
				compiled: compiled{file: file},
				diag:     &bytes.Buffer{},
				err:      notAScenarioError(file),
			}
			continue
		}
		diag := &bytes.Buffer{}
		c, err := loadArtFile(file, diag)
		// loadArtFile answers a zero compiled when the file would not
		// compile, and every report below needs the path either way.
		c.file = file
		attempts[i] = fileAttempt{ok: err == nil, compiled: c, err: err, diag: diag}
	}

	var loaded []compiled
	for _, a := range attempts {
		if a.ok {
			loaded = append(loaded, a.compiled)
		}
	}
	// nil is os.LookupEnv: the real process environment, which is where the
	// .env file was loaded to. A seam here would have had no caller -- the
	// gate's own tests pass their lookup to envGate directly.
	faults := envGate(loaded, nil)
	if len(faults) > 0 {
		rt.envFaults = faults
	}

	// A file that would not compile is reported whether or not the gate
	// tripped: that fault belongs to the file and was known before the gate
	// ran. A file that compiled only runs if the gate found nothing, which is
	// the one new case: every scenario that would have run is simply never
	// started, and the run ends with none of them in it.
	for _, a := range attempts {
		if a.diag.Len() > 0 {
			diagOut.Write(a.diag.Bytes()) //nolint:errcheck // the console report below already surfaces a run that cannot report
		}
		if !a.ok {
			failScenario(run, rep, rt.events, a.file, a.err)
			continue
		}
		if len(faults) > 0 {
			continue
		}
		runCompiled(context.Background(), rt, a.compiled, run, rep)
	}
	run.Finish()
	// Finish derives Status from the scenarios, and a gated run can hold
	// scenarios that all passed -- the files that happened to need nothing
	// absent -- or none at all. Either way the run did not do what it was
	// asked: force the status a reader of the console summary or the JSON
	// report sees to match the non-zero exit envGateError already gives the
	// process, so neither one calls this a pass.
	if len(faults) > 0 {
		run.Status = result.StatusError
		run.Error = envGateError(faults).Error()
	}
	// After the gate has had its say about the status, so run-end says what
	// the report and the exit code say.
	rt.events.RunEnd(run)
	return run
}

// fileAttempt is the outcome of compiling one file, kept in file order until
// the environment gate has had its say and the run is ready to record every
// outcome -- and print every diagnostic -- at once.
//
// The path is the embedded compiled's, set for a file that would not compile
// too: a second `file` field beside it would shadow that one, and the two could
// then disagree about which file this is.
type fileAttempt struct {
	ok bool
	compiled
	err error
	// diag is a pointer because compileArt writes to it through an io.Writer
	// while the attempt is being built; a value here would be copied into the
	// slice and the diagnostics written to the copy that was thrown away.
	diag *bytes.Buffer
}

// notAScenarioError is what a path artemis will not run says. It names the one
// way out, with the command spelled for this file, because the reader's next
// move is to run it.
func notAScenarioError(file string) error {
	if strings.EqualFold(filepath.Ext(file), ".yaml") || strings.EqualFold(filepath.Ext(file), ".yml") {
		out := strings.TrimSuffix(file, filepath.Ext(file)) + artExt
		return fmt.Errorf("%s: artemis no longer runs YAML scenarios -- convert it with \"artemis migrate -f %s -o %s\"", file, file, out)
	}
	return fmt.Errorf("%s: not a scenario file (artemis runs %s files)", file, artExt)
}

// failScenario records a file artemis could not load as one errored scenario.
//
// No name to give it: the file did not load, so all anyone knows about this
// scenario is where it lives. On the event stream that is a scenario-start
// with no name and no steps, and an errored scenario-end with the reason.
func failScenario(run *result.RunResult, rep *report.Console, events *report.Events, file string, err error) {
	scenario := run.NewScenario("", file)
	rep.Scenario(scenario)
	events.ScenarioStart(scenario, nil)
	scenario.Fail(0, err)
	rep.ScenarioFailed(scenario)
	events.ScenarioEnd(scenario)
	logger.Logger.Error("Could not load a scenario", "file", file, "error", err.Error())
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

// screenshotsFlag is the flag naming where a failed browser step's screenshot
// goes. Empty means do not take any.
const screenshotsFlag = "screenshots"

// shotDir is where a failed browser step's screenshot goes: --screenshots, or
// the default.
//
// The empty string turns them off, which is why this reads the flag rather than
// relying on its default -- an explicit `--screenshots ""` has to be
// distinguishable from not passing it, and with cobra's own default it is: the
// value is what the user said.
//
// A command with no such flag -- there is none today, but `test` grew its flags
// separately once already -- gets the default rather than an error, because a
// missing flag is a wiring mistake and failing a run over it would be the worst
// possible report of one.
func shotDir(cmd *cobra.Command) string {
	dir, err := cmd.Flags().GetString(screenshotsFlag)
	if err != nil {
		return browserstep.DefaultDir
	}
	return dir
}
