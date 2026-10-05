package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/lower"
	"artemis/pkg/eval"
)

// compiled is one file that compiled: the scenarios to run, and the
// environment variables the file names.
type compiled struct {
	file      string
	scenarios []*lower.Scenario
	needs     []check.EnvNeed
}

// envFault is one variable a run needs and the environment does not supply,
// with the first place that needs it.
type envFault struct {
	Name string
	File string
	Line int
}

// envGate is the check before the run: which of the variables that these files
// name have no value.
//
// It covers the whole run rather than one file at a time, which is a
// deliberate break with runFiles's contract that "a file artemis cannot load
// is recorded as an errored scenario and the rest still run". The kinds of
// fault differ. A file that will not compile is a fault in that file, so the
// other files are still worth running. An absent variable is a fault in the
// environment, which every file of the run shares -- so running the files that
// happen not to name it means a suite that creates real orders from its first
// two files and then reports the third one's missing variable.
//
// Every fault, not the first: a run with four absent variables takes one run
// to fix rather than four.
//
// One exception, and SPEC.md's "nothing executes" is qualified by it: the
// needs come from the files that compiled, so a file that will not compile
// contributes none of its env() names. A suite of a broken file that needs
// API_URL plus a good file that needs nothing runs the good file for real and
// never mentions API_URL. Correcting the typo is what makes the gate trip --
// two runs for two faults, with the good file's side effects in between.
//
// That is accepted rather than designed around. The alternative is reading
// needs out of a tree the checker rejected, whose env() calls may be
// half-parsed, and naming variables for a file nobody can run yet. A file that
// will not compile is already reported with its line and its caret, and
// correcting it is the reader's next move either way.
func envGate(files []compiled, lookup func(string) (string, bool)) []envFault {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var faults []envFault
	seen := map[string]bool{}
	for _, f := range files {
		for _, n := range f.needs {
			if seen[n.Name] {
				continue
			}
			if v, ok := lookup(n.Name); ok && eval.HasValue(v) {
				continue
			}
			seen[n.Name] = true
			faults = append(faults, envFault{Name: n.Name, File: f.file, Line: n.Span.Line})
		}
	}
	return faults
}

// envGateReport writes the block a reader acts on: every absent variable, the
// first place that needs it, and which env file artemis read.
//
// The env file is named because the usual cause is the file: the key is there
// and the value is not, or the run read a different file than the reader
// thinks.
func envGateReport(w io.Writer, faults []envFault, envFile string) {
	if len(faults) == 0 {
		return
	}
	fmt.Fprintln(w, "artemis: the environment is not complete")
	fmt.Fprintln(w)
	width := 0
	for _, f := range faults {
		if len(f.Name) > width {
			width = len(f.Name)
		}
	}
	for _, f := range faults {
		fmt.Fprintf(w, "  %-*s  %s:%d\n", width, f.Name, f.File, f.Line)
	}
	fmt.Fprintln(w)
	if envFile != "" {
		fmt.Fprintf(w, "read %s; ", envFile)
	}
	fmt.Fprintf(w, "no value for %s\n", plural(len(faults), "name", "names"))
	fmt.Fprintln(w, "nothing ran")
}

// envGateError is the one line attached to the non-zero exit. It is what
// survives when only stderr is kept, so it names the variables.
func envGateError(faults []envFault) error {
	if len(faults) == 0 {
		return nil
	}
	names := make([]string, 0, len(faults))
	for _, f := range faults {
		names = append(names, f.Name)
	}
	return errors.New("the environment is not complete: no value for " + strings.Join(names, ", "))
}
