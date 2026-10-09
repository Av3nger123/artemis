package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"artemis/pkg/report"
	"artemis/pkg/result"
)

// reportFlag is the flag a user passes to ask for a machine-readable report, and
// reportStdout is the path that means "write it to stdout".
const (
	reportFlag   = "report"
	reportStdout = "-"
)

// eventsFlag is the flag that streams a run as it happens, and eventsNDJSON is
// its one format: one JSON object per line, on stdout.
const (
	eventsFlag   = "events"
	eventsNDJSON = "ndjson"
)

// reportWriters maps a --report format to the function that writes it. Adding a
// format is adding a line here and the writer it names; nothing else in the CLI
// has to know about it.
var reportWriters = map[string]func(io.Writer, *result.RunResult) error{
	"json":  report.WriteJSON,
	"junit": report.WriteJUnit,
}

// reportTarget is one report the user asked for: a format and where it goes. An
// empty path means stdout.
type reportTarget struct {
	format string
	path   string
}

// stdout reports whether this target is written to the command's stdout rather
// than to a file.
func (t reportTarget) stdout() bool { return t.path == "" || t.path == reportStdout }

// reportFormats is the known formats, in order, for an error message.
func reportFormats() []string {
	formats := make([]string, 0, len(reportWriters))
	for f := range reportWriters {
		formats = append(formats, f)
	}
	sort.Strings(formats)
	return formats
}

// parseReports turns the --report values into targets.
//
// The grammar is `format[=path]`: a bare format, or `=-`, goes to stdout, and
// anything else is a file. It is checked before a single scenario runs -- a
// mistyped format must not cost a suite run, and a user who asked for a report
// that cannot be named has not been told anything until the command refuses.
//
// A format given twice, and two formats aimed at stdout, are both refused: the
// first is a mistake with a silent winner, and the second would interleave two
// documents on one stream, which is exactly what the flag exists to avoid.
func parseReports(values []string) ([]reportTarget, error) {
	var targets []reportTarget
	seen := map[string]bool{}
	stdoutTaken := ""
	for _, raw := range values {
		format, path, _ := strings.Cut(raw, "=")
		format = strings.TrimSpace(format)
		if format == "" {
			return nil, fmt.Errorf("--%s %q: no format given, want one of %s",
				reportFlag, raw, strings.Join(reportFormats(), ", "))
		}
		if _, ok := reportWriters[format]; !ok {
			return nil, fmt.Errorf("--%s %q: unknown format %q, want one of %s",
				reportFlag, raw, format, strings.Join(reportFormats(), ", "))
		}
		if seen[format] {
			return nil, fmt.Errorf("--%s %s given twice", reportFlag, format)
		}
		seen[format] = true

		target := reportTarget{format: format, path: path}
		if target.stdout() {
			if stdoutTaken != "" {
				return nil, fmt.Errorf("--%s %s and --%s %s both write to stdout; give one of them a path",
					reportFlag, stdoutTaken, reportFlag, format)
			}
			stdoutTaken = format
		}
		targets = append(targets, target)
	}
	return targets, nil
}

// anyToStdout reports whether any target goes to stdout, which is what decides
// whether the console report moves to stderr.
func anyToStdout(targets []reportTarget) bool {
	for _, t := range targets {
		if t.stdout() {
			return true
		}
	}
	return false
}

// writeReports writes every target, in the order the user gave them, and stops
// at the first one that fails.
//
// A report that cannot be written fails the command even when the run passed: a
// CI job whose report silently vanished is worse off than one that went red.
func writeReports(stdout io.Writer, targets []reportTarget, run *result.RunResult) error {
	for _, t := range targets {
		write := reportWriters[t.format]
		if t.stdout() {
			if err := write(stdout, run); err != nil {
				return err
			}
			continue
		}
		if err := writeReportFile(write, t, run); err != nil {
			return err
		}
	}
	return nil
}

// writeReportFile writes one target to its path, truncating what was there. A
// report is the outcome of this run, so appending to the last one's would make
// the file unparseable.
func writeReportFile(write func(io.Writer, *result.RunResult) error, t reportTarget, run *result.RunResult) error {
	file, err := os.OpenFile(t.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s report %s: %w", t.format, t.path, err)
	}
	if err := write(file, run); err != nil {
		// The close error is beside the point once the write has failed, and
		// the write error is the one that says what went wrong.
		_ = file.Close()
		return fmt.Errorf("writing %s report %s: %w", t.format, t.path, err)
	}
	if err := file.Close(); err != nil {
		// A write that only fails on close is a report that is there but
		// truncated, and that must not pass for a written one.
		return fmt.Errorf("closing %s report %s: %w", t.format, t.path, err)
	}
	return nil
}

// parseEvents reads --events: empty is off, "ndjson" is on, and anything else
// is refused.
//
// The stream is written to stdout, so a report aimed at stdout as well is
// refused for the reason two stdout reports are: two documents interleaved on
// one stream are neither of them readable. A report with a path is fine, and
// is how a caller gets both the live stream and the record of the run.
//
// Like parseReports it is checked before discovery, so a mistake costs no run.
func parseEvents(value string, targets []reportTarget) (bool, error) {
	if value == "" {
		return false, nil
	}
	if value != eventsNDJSON {
		return false, fmt.Errorf("--%s %q: unknown format, want %s", eventsFlag, value, eventsNDJSON)
	}
	for _, t := range targets {
		if t.stdout() {
			return false, fmt.Errorf("--%s %s and --%s %s both write to stdout; give the report a path, as in --%s %s=<path>",
				eventsFlag, eventsNDJSON, reportFlag, t.format, reportFlag, t.format)
		}
	}
	return true, nil
}
