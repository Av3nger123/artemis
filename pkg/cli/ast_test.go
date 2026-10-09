package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"artemis/pkg/dsl/encode"
)

// The document goes to stdout and nothing else does, because the whole point of
// `artemis ast` is being piped: into a UI, into jq, into `artemis ast
// --from-json`.
func TestAstWritesTheTreeToStdout(t *testing.T) {
	path := writeArt(t, "checkout.art", cleanArt)

	stdout, stderr, err := executeArgs(t, "ast", "-f", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if doc["schemaVersion"] != float64(encode.SchemaVersion) {
		t.Errorf("schemaVersion = %v, want %d", doc["schemaVersion"], encode.SchemaVersion)
	}
	if doc["file"] != path {
		t.Errorf("file = %v, want %q", doc["file"], path)
	}
	if !strings.HasSuffix(stdout, "}\n") {
		t.Errorf("the document does not end with a newline:\n%q", stdout[max(0, len(stdout)-20):])
	}
	// The checker's labels are what distinguish this from a bare parse dump.
	for _, want := range []string{`"class": "simple"`, `"stepType": "api"`, `"scope":`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the document has no %s", want)
		}
	}
}

// The pair composes: `ast -f x.art | ast --from-json` is `fmt x.art`. That
// equality is the whole contract in one line -- the JSON carries what canonical
// source carries -- and it is checked through the commands rather than through
// the package so that the plumbing is in it too.
func TestAstRoundTripsThroughTheCommands(t *testing.T) {
	path := writeArt(t, "checkout.art", uglyArt)

	doc, stderr, err := executeArgs(t, "ast", "-f", path)
	if err != nil {
		t.Fatalf("ast -f: %v\nstderr:\n%s", err, stderr)
	}
	back, stderr, err := executeStdin(t, doc, "ast", "--from-json")
	if err != nil {
		t.Fatalf("ast --from-json: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	formatted, _, err := executeArgs(t, "fmt", path)
	if err != nil {
		t.Fatal(err)
	}
	if back != formatted {
		t.Errorf("the round trip does not agree with fmt\n--- fmt ---\n%s\n--- ast --from-json ---\n%s",
			formatted, back)
	}
}

// -f is accepted with --from-json too, for anyone holding a tree on disk.
func TestAstFromJSONReadsAFile(t *testing.T) {
	art := writeArt(t, "checkout.art", cleanArt)
	doc, _, err := executeArgs(t, "ast", "-f", art)
	if err != nil {
		t.Fatal(err)
	}
	tree := writeArt(t, "tree.json", doc)

	stdout, stderr, err := executeArgs(t, "ast", "--from-json", "-f", tree)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if !strings.HasPrefix(stdout, "# A scenario that checks clean.\nscenario \"checkout\" {") {
		t.Errorf("stdout does not start with the file's own comment and scenario:\n%s", stdout)
	}
}

// A file with an error produces no document: the diagnostics go to stderr, the
// exit status is non-zero, and stdout stays empty so that `artemis ast -f x.art
// > tree.json` never leaves a half-described tree on disk.
func TestAstRefusesAFileWithAnError(t *testing.T) {
	path := writeArt(t, "broken.art", strings.Replace(cleanArt, "expect status == 200", "expect statu == 200", 1))

	stdout, stderr, err := executeArgs(t, "ast", "-f", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing for a file with an error", stdout)
	}
	for _, want := range []string{`unknown field "statu"`, `did you mean "status"?`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not mention %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(err.Error(), "1 error in") {
		t.Errorf("Execute() = %v, want it to name the count and the file", err)
	}
}

// A tree holding a node that did not parse is refused, naming what is wrong:
// encoding is total so that `artemis ast` can describe a file mid-edit, and
// decoding is not, because a UI serialises a complete tree.
func TestAstFromJSONRefusesAnIncompleteTree(t *testing.T) {
	path := writeArt(t, "broken.art", "scenario \"s\" {\n  step \"one\" {\n    get \"/a\"\n    expect == 200\n  }\n}\n")

	// -f on a file with an error writes nothing, so the document for this test
	// is built from the package rather than from the command.
	src, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatal(err)
	}
	u := frontEnd(path, string(src))
	tree, info, bag := u.Tree, u.Info, u.Bag
	if !bag.HasErrors() {
		t.Fatalf("the fixture parses clean, so this test asserts nothing")
	}
	doc, err := encode.Encode(tree, info)
	if err != nil {
		t.Fatalf("encoding a recovered file should work: %v", err)
	}

	stdout, _, err := executeStdin(t, string(doc), "ast", "--from-json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(err.Error(), "did not parse") {
		t.Errorf("Execute() = %v, want it to say the tree holds a node that did not parse", err)
	}
}

// Empty stdin is an error rather than an empty file, because it is nearly
// always the previous command in the pipe having failed.
func TestAstFromJSONNeedsSomethingOnStdin(t *testing.T) {
	_, _, err := executeStdin(t, "", "ast", "--from-json")
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "nothing on stdin") {
		t.Errorf("Execute() = %v, want it to say stdin was empty", err)
	}
}

// The two misuses worth their own message: no file at all, and a YAML file.
// YAML has no tree -- the DSL's front end is what builds one -- so saying so is
// better than describing an empty document.
func TestAstMisuse(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no file", []string{"ast"}, "artemis ast needs a file"},
		{"a YAML file", []string{"ast", "-f", "suite.yaml"}, "artemis ast reads .art files"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, _, err := executeArgs(t, c.args...)
			if err == nil {
				t.Fatal("Execute() = nil, want an error")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Execute() = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

// executeStdin is executeArgs with a document on the command's stdin.
func executeStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	initOnce.Do(Init)
	resetFrontEndFlags(t)

	var out, errOut bytes.Buffer
	RootCmd.SetArgs(args)
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&errOut)
	RootCmd.SetIn(strings.NewReader(stdin))
	t.Cleanup(func() {
		RootCmd.SetOut(os.Stderr)
		RootCmd.SetErr(os.Stderr)
		RootCmd.SetIn(os.Stdin)
		RootCmd.SetArgs(nil)
	})

	runErr := RootCmd.Execute()
	return out.String(), errOut.String(), runErr
}
