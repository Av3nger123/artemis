package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// cleanArt is a file with nothing wrong with it: every construct `artemis
// parse` has to accept, and no fault anywhere. It is written out rather than
// read from pkg/dsl's fixtures so that a change to those fixtures cannot
// quietly change what this test means.
const cleanArt = `# A scenario that checks clean.
flow checkout {
  var url = env("API_URL")

  step login {
    post "${url}/token" {
      header "Content-Type" = "application/json"
      body = {"username": "alice"}
    }
    expect status == 200
    var token = body.data.access_token
  }

  step orders {
    get "${url}/orders" {
      header "Authorization" = "Bearer ${token}"
      query "limit" = 10
    }
    timeout = "5s"
    retry { times = 3, delay = "2s" }

    expect status == 200
    expect body.data.count > 0
  }
}

scenario checkout {}
`

// manyErrorsArt holds four unrelated faults with well-formed code either side
// of each, after pkg/dsl/testdata/invalid/many_errors.art -- a statement that
// is not a declaration, a request field with no value, a chained comparison,
// and a step with no action block. All four must be reported: a front end that
// stopped at the first would cost one edit cycle per mistake.
const manyErrorsArt = `flow many {
  var url = env("API_URL")
  nonsense here
  var kept = 1

  step survives_the_bad_field as "survives the bad field" {
    post "${url}/orders" {
      header "Content-Type" = "application/json"
      query
      body = {"sku": "A-1"}
    }
    expect status == 201
  }

  step survives_the_chain as "survives the chain" {
    get "/orders"
    expect body.data.count == 1 == true
    var token = body.data.token
  }

  step has_nothing_to_run as "has nothing to run" {
    expect status == 200
  }
}

scenario first {}
`

// writeArt writes src to a file named name in a fresh temp directory and
// returns the path.
//
// A src that is a flow followed by the scenarios that run it -- the two files
// a user writes, in one string, so a test reads top to bottom -- is written as
// those two files: the flow, up to the first line that starts a scenario, goes
// to <stem>.flow.art beside name, and the scenarios go to name under the
// header that names it. The flow's lines keep their numbers, so a diagnostic a
// test asserts on reads as the source it wrote. Any other src is written as
// it is.
func writeArt(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	writeSplit(t, path, src)
	return path
}

// writeSplit writes src to path, split into a flow file and a scenario file
// when it is a flow followed by scenarios. See writeArt.
func writeSplit(t *testing.T, path, src string) {
	t.Helper()
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	flow, scenarios, ok := splitFlow(src)
	if !ok {
		write(path, src)
		return
	}
	base := filepath.Base(path)
	flowName := strings.TrimSuffix(base, filepath.Ext(base)) + ".flow" + artExt
	write(filepath.Join(filepath.Dir(path), flowName), flow)
	write(path, "flow "+strconv.Quote(flowName)+"\n\n"+scenarios)
}

// flowOf is the flow file writeArt wrote beside the scenario file at path.
func flowOf(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".flow" + artExt
}

// splitFlow cuts src at the first line that starts a scenario, when a line
// before it starts a flow: `flow name {`.
func splitFlow(src string) (flow, scenarios string, ok bool) {
	sawFlow := false
	at := 0
	for _, line := range strings.SplitAfter(src, "\n") {
		switch {
		case strings.HasPrefix(line, "flow ") && !strings.HasPrefix(line, `flow "`):
			sawFlow = true
		case strings.HasPrefix(line, "scenario ") && sawFlow:
			return src[:at], src[at:], true
		}
		at += len(line)
	}
	return "", "", false
}

// resetFrontEndFlags puts the flags of the commands this file exercises back to
// their defaults. They are package-level values, so a flag one test set is
// still set for the next one: a sticky `fmt -w` would rewrite a file a test
// expected to be left alone, and a required flag that is still Changed from the
// last test stops being required. It is the counterpart of resetRunFlags, which
// covers the commands that run scenarios.
func resetFrontEndFlags(t *testing.T) {
	t.Helper()
	for _, cmd := range []*cobra.Command{parseCmd, astCmd, fmtCmd, grammarCmd, migrateCmd, generateCmd, expandCmd} {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatalf("resetting %s --%s to %q: %v", cmd.Name(), f.Name, f.DefValue, err)
			}
			f.Changed = false
		})
	}
}

// executeArgs runs the real command through RootCmd and returns its two streams
// separately. The separation is the thing under test as much as the content is:
// diagnostics belong on stderr, so that stdout carries documents only.
func executeArgs(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	initOnce.Do(Init)
	resetFrontEndFlags(t)

	var out, errOut bytes.Buffer
	RootCmd.SetArgs(args)
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		RootCmd.SetOut(os.Stderr)
		RootCmd.SetErr(os.Stderr)
		RootCmd.SetArgs(nil)
	})

	runErr := RootCmd.Execute()
	return out.String(), errOut.String(), runErr
}

// A clean file: exit zero, one line saying so on stdout, and nothing at all on
// stderr. A command whose only job is to tell you something has to say
// something when the answer is yes.
func TestParseArtAcceptsACleanFile(t *testing.T) {
	path := writeArt(t, "checkout.art", cleanArt)

	stdout, stderr, err := executeArgs(t, "parse", "-f", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if want := path + ": ok\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: a clean file has no diagnostics", stderr)
	}
}

// The issue's main case: several errors in one file, all of them reported, and
// a non-zero exit. The faults are asserted individually, because a count alone
// would pass for four copies of one cascade.
func TestParseArtReportsEveryErrorAndFails(t *testing.T) {
	path := writeArt(t, "many_errors.art", manyErrorsArt)

	stdout, stderr, err := executeArgs(t, "parse", "-f", path)
	if err == nil {
		t.Fatalf("Execute() = nil, want an error so the process exits non-zero\nstderr:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: a file with errors is not ok", stdout)
	}

	for _, want := range []string{
		`expected "config", "var", "step", "use", "setup", "teardown" or a block, found "nonsense"`,
		`expected "=" and a value after "query"`,
		`"==" cannot be chained with another comparison`,
		`step has_nothing_to_run has no action block`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not report %q; the front end stopped early\n%s", want, stderr)
		}
	}

	// The rendered form, not just the message: the caret line and the hint are
	// what make a diagnostic actionable, and this command is where they reach
	// a person.
	if !strings.Contains(stderr, "^^^^^^^^") {
		t.Errorf("stderr has no caret line:\n%s", stderr)
	}
	if !strings.Contains(stderr, "hint: a step does one of") {
		t.Errorf("stderr has no hint:\n%s", stderr)
	}

	// The reason attached to the exit names the count and the file, because it
	// is what survives when a log keeps only the last line.
	if got := err.Error(); !strings.Contains(got, "4 errors in") || !strings.Contains(got, path) {
		t.Errorf("Execute() = %q, want it to name four errors and the path", got)
	}
}

// Diagnostics are reported in the order they appear in the file, however many
// passes found them: a reader walks the file top to bottom.
func TestParseArtReportsInFileOrder(t *testing.T) {
	path := writeArt(t, "many_errors.art", manyErrorsArt)

	_, stderr, err := executeArgs(t, "parse", "-f", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	order := []string{"nonsense", `after "query"`, "cannot be chained", "no action block"}
	at := -1
	for _, want := range order {
		i := strings.Index(stderr, want)
		if i < 0 {
			t.Fatalf("stderr does not report %q:\n%s", want, stderr)
		}
		if i < at {
			t.Errorf("%q is out of file order:\n%s", want, stderr)
		}
		at = i
	}
}

// Parsing executes nothing. A step that would run a command if anything ran it
// is the cheapest way to assert that: the file names a command that would
// create a file, and the file must not be there.
func TestParseArtRunsNothing(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	path := filepath.Join(dir, "deploy.art")
	src := `flow deploy {
  step seed {
    run "touch" {
      args = ["` + filepath.ToSlash(marker) + `"]
    }
    expect exit_code == 0
  }
}

scenario deploy {}
`
	writeSplit(t, path, src)

	if _, stderr, err := executeArgs(t, "parse", "-f", path); err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the step ran: `artemis parse` must execute nothing")
	}
}

// A file that is not there is an error about the file, not a diagnostic about
// its contents.
func TestParseArtOnAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.art")

	_, stderr, err := executeArgs(t, "parse", "-f", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Execute() = %q, want it to name the path", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: there was no source to diagnose", stderr)
	}
}

// The YAML branch is gone: `artemis parse` reads .art and nothing else
// (ART-40). A YAML path is refused with the one way out, rather than lexed
// into a page of diagnostics about a file that was never meant to be one.
func TestParseRefusesYAMLAndNamesMigrate(t *testing.T) {
	path := writeArt(t, "scenario.yaml", `name: "health"
type: functional
`)

	stdout, _, err := executeArgs(t, "parse", "-f", path)
	if err == nil {
		t.Fatalf("Execute() = nil, want a refusal\nstdout:\n%s", stdout)
	}
	for _, want := range []string{"scenario.yaml", "artemis migrate", "scenario.art"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing: the file was not checked", stdout)
	}
}

// The flag bug the v3 audit found: the required-flag marker sat on testCmd, so
// `artemis generate` had no required flag at all and failed opening "".
func TestGenerateRequiresAFile(t *testing.T) {
	_, _, err := executeArgs(t, "generate")
	if err == nil {
		t.Fatal("Execute() = nil, want a flag-validation error")
	}
	if !strings.Contains(err.Error(), "file") {
		t.Errorf("Execute() = %q, want it to name the missing flag", err)
	}
}
