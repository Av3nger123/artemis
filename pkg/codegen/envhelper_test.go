package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/eval"
)

// The two art_env helpers are *run*, not read.
//
// Everything else about them was asserted by two substring checks on generated
// text and by the parity corpus -- and every corpus scenario sets the variable
// it reads, so nothing in the suite ever reached the raise branch, the trim
// rule or the default branch of either helper. On a branch whose claim is that
// the interpreter, Python and JavaScript agree about env(), that left both of
// the new agreements asserted by nobody. TestJSExecutionParity would not have
// closed it either: it needs a global vitest, and skips without one.
//
// A helper's body is a Go string in a table, so neither pytest nor vitest nor
// Playwright is involved. The source goes into a temporary .py or .mjs with a
// driver appended, and python3 or node runs it. A missing interpreter is a skip
// naming it, the way `make lint` treats a missing linter.
//
// The sentence for an absent variable comes from eval.Absent, so a change to
// the interpreter's wording fails these tests rather than drifting past them.

// nonStringDefault is the interpreter's wording for a default value that is
// not a string, as far as a helper can reproduce it.
//
// eval appends " at <the expression>" -- `env()'s argument must be a string,
// got number at p` -- which a helper cannot know: it has the value, not the
// source text. So this is the part both sides must share, and
// TestEnvNonStringDefaultNamesTheType in pkg/eval pins the interpreter's half
// of it.
const nonStringDefault = "env()'s argument must be a string, got "

// envHelperCase is one question asked of a helper, written so that the Python
// and the JavaScript driver can both be generated from one list. value is what
// the variable holds, with absent meaning "not set at all".
type envHelperCase struct {
	what  string
	value string
	// call is the helper call, in the syntax both languages happen to share
	// for these arguments.
	call string
	// want is the string the call must return, when wantErr is empty.
	want string
	// wantErr is the text the thrown error's message must hold.
	wantErr string
	// wantKind is "type" when the error must be a TypeError, and empty when
	// any error will do -- "no value" is an AssertionError in Python and an
	// Error in JavaScript, because each is what that language's test runner
	// reports as a failed test.
	wantKind string
	// skipPython marks a case Python cannot be asked: its None is how
	// "no default argument" is spelled, so art_env(name, None) is
	// env("NAME") rather than env("NAME", null).
	skipPython bool
}

const envHelperVar = "ARTEMIS_HELPER_TEST"

// absent is the sentinel for "the variable is not set", which is a different
// case from the empty value on the line below it.
const absent = "\x00"

func envHelperCases() []envHelperCase {
	noValue := eval.Absent(envHelperVar).Error()
	return []envHelperCase{
		{what: "an absent variable", value: absent, call: `art_env("` + envHelperVar + `")`, wantErr: noValue},
		{what: "an empty value", value: "", call: `art_env("` + envHelperVar + `")`, wantErr: noValue},
		{what: "a value of only space characters", value: "   ", call: `art_env("` + envHelperVar + `")`, wantErr: noValue},
		{what: "a value with space at each end", value: "  http://x  ", call: `art_env("` + envHelperVar + `")`, want: "  http://x  "},
		{what: "a default for an absent variable", value: absent, call: `art_env("` + envHelperVar + `", "8080")`, want: "8080"},
		{what: "a default for a blank value", value: "  ", call: `art_env("` + envHelperVar + `", "8080")`, want: "8080"},
		{what: "an empty default", value: absent, call: `art_env("` + envHelperVar + `", "")`, want: ""},
		{
			what: "a number as the default", value: absent,
			call: `art_env("` + envHelperVar + `", 8080)`,
			// This is the case that was returning 8080 in Python and, for a
			// null, returning null in JavaScript -- which art_render turns
			// into the string "null" and puts in a URL.
			wantErr: nonStringDefault + "number", wantKind: "type",
		},
		{
			what: "a null as the default", value: absent,
			call:       `art_env("` + envHelperVar + `", null)`,
			wantErr:    nonStringDefault + "null",
			wantKind:   "type",
			skipPython: true,
		},
	}
}

// The Python helper, run.
func TestPythonEnvHelperAgreesWithTheInterpreter(t *testing.T) {
	python := python3(t)

	var b strings.Builder
	b.WriteString("import os\n\n")
	b.WriteString(byName[helperEnv].src)
	b.WriteString(`
failures = []


def record(what, problem):
    failures.append("%s: %s" % (what, problem))


def returns(what, fn, want):
    try:
        got = fn()
    except BaseException as e:  # noqa: BLE001 - any error here is the failure
        record(what, "raised %s(%s), want %r" % (type(e).__name__, e, want))
        return
    if got != want:
        record(what, "returned %r, want %r" % (got, want))


def raises(what, fn, want, kind):
    try:
        fn()
    except BaseException as e:  # noqa: BLE001 - the kind is checked below
        if kind == "type" and not isinstance(e, TypeError):
            record(what, "raised %s(%s), want a TypeError" % (type(e).__name__, e))
            return
        if want not in str(e):
            record(what, "raised %r, want it to hold %r" % (str(e), want))
        return
    record(what, "did not raise")


def have(value):
    if value is None:
        os.environ.pop(` + pyQuote(envHelperVar) + `, None)
    else:
        os.environ[` + pyQuote(envHelperVar) + `] = value

`)
	for _, c := range envHelperCases() {
		if c.skipPython {
			continue
		}
		b.WriteString("have(" + pyValue(c.value) + ")\n")
		if c.wantErr != "" {
			fmt.Fprintf(&b, "raises(%s, lambda: %s, %s, %s)\n",
				pyQuote(c.what), c.call, pyQuote(c.wantErr), pyQuote(c.wantKind))
			continue
		}
		fmt.Fprintf(&b, "returns(%s, lambda: %s, %s)\n", pyQuote(c.what), c.call, pyQuote(c.want))
	}
	b.WriteString(`
if failures:
    raise SystemExit("\n".join(failures))
`)

	path := filepath.Join(t.TempDir(), "art_env_check.py")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(python, path).CombinedOutput() //nolint:gosec // python came from exec.LookPath
	if err != nil {
		t.Errorf("the python art_env does not agree with the interpreter:\n%s\n%v", out, err)
	}
}

// The JavaScript helper, run.
func TestJSEnvHelperAgreesWithTheInterpreter(t *testing.T) {
	node := nodeBinary(t)

	var b strings.Builder
	b.WriteString(jsByName[jsHelperEnv].src)
	b.WriteString(`
const failures = [];

function record(what, problem) {
  failures.push(what + ": " + problem);
}

function returns(what, fn, want) {
  let got;
  try {
    got = fn();
  } catch (e) {
    record(what, "threw " + e + ", want " + JSON.stringify(want));
    return;
  }
  if (got !== want) {
    record(what, "returned " + JSON.stringify(got) + ", want " + JSON.stringify(want));
  }
}

function throwsWith(what, fn, want, kind) {
  try {
    fn();
  } catch (e) {
    if (kind === "type" && !(e instanceof TypeError)) {
      record(what, "threw " + e.constructor.name + " (" + e.message + "), want a TypeError");
      return;
    }
    if (!String(e.message).includes(want)) {
      record(what, "threw " + JSON.stringify(e.message) + ", want it to hold " + JSON.stringify(want));
    }
    return;
  }
  record(what, "did not throw");
}

function have(value) {
  if (value === null) {
    delete process.env[NAME];
  } else {
    process.env[NAME] = value;
  }
}

`)
	fmt.Fprintf(&b, "const NAME = %s;\n", jsQuote(envHelperVar))
	for _, c := range envHelperCases() {
		b.WriteString("have(" + jsValue(c.value) + ");\n")
		if c.wantErr != "" {
			fmt.Fprintf(&b, "throwsWith(%s, () => %s, %s, %s);\n",
				jsQuote(c.what), c.call, jsQuote(c.wantErr), jsQuote(c.wantKind))
			continue
		}
		fmt.Fprintf(&b, "returns(%s, () => %s, %s);\n", jsQuote(c.what), c.call, jsQuote(c.want))
	}
	b.WriteString(`
if (failures.length) {
  console.error(failures.join("\n"));
  process.exit(1);
}
`)

	path := filepath.Join(t.TempDir(), "art_env_check.mjs")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput() //nolint:gosec // node came from exec.LookPath
	if err != nil {
		t.Errorf("the javascript art_env does not agree with the interpreter:\n%s\n%v", out, err)
	}
}

// pyQuote writes a Go string as a Python literal. %q is Go's quoting, which
// Python reads the same way for the ASCII these cases hold; the JavaScript
// side uses the emitter's own jsQuote.
func pyQuote(s string) string { return fmt.Sprintf("%q", s) }

// pyValue and jsValue write a case's value as the argument to have(): the
// language's own null for "not set at all", a string otherwise.
func pyValue(s string) string {
	if s == absent {
		return "None"
	}
	return pyQuote(s)
}

func jsValue(s string) string {
	if s == absent {
		return "null"
	}
	return jsQuote(s)
}
