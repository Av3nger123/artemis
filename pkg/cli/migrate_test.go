package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/print"
)

// writeYAML writes src to a file in a fresh temp directory, the YAML
// counterpart of writeArt.
func writeYAML(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// smallYAML is one api step, with a comment above the file and above the step,
// so the command tests exercise both carried positions.
const smallYAML = `# what this file is for
name: "ping"
type: functional
variables: []
steps:
  # what this step does
  - name: "ping"
    type: api
    request:
      url: "%SERVER%/ping"
      method: "GET"
    response:
      status_code: 200
`

// Without -o: the .art source goes to stdout, nothing goes to stderr, and the
// YAML file is left exactly as it was. Migration never deletes its input.
func TestMigratePrintsToStdout(t *testing.T) {
	path := writeYAML(t, "ping.yaml", smallYAML)

	stdout, stderr, err := executeArgs(t, "migrate", "-f", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	want := `# what this file is for
scenario "ping" {
  # what this step does
  step "ping" {
    get "%SERVER%/ping"
    expect status == 200
  }
}
`
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	on, err := os.ReadFile(path) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != smallYAML {
		t.Errorf("the YAML file changed:\n%s", on)
	}
}

// -o writes the file and prints nothing, so a migration that is redirected and
// one that is written to a path produce the same bytes.
func TestMigrateWritesTheFileWithOut(t *testing.T) {
	path := writeYAML(t, "ping.yaml", smallYAML)
	out := filepath.Join(filepath.Dir(path), "ping.art")

	stdout, stderr, err := executeArgs(t, "migrate", "-f", path, "-o", out)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want nothing: -o writes to the file", stdout, stderr)
	}

	written, err := os.ReadFile(out) //nolint:gosec // a path this test named
	if err != nil {
		t.Fatalf("reading %s: %v", out, err)
	}
	printed, _, err := executeArgs(t, "migrate", "-f", path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != printed {
		t.Errorf("-o wrote\n%s\nbut stdout had\n%s", written, printed)
	}

	// And what it wrote is a file the front end accepts, which is the whole
	// promise of the command.
	if _, _, err := executeArgs(t, "parse", "-f", out); err != nil {
		t.Errorf("parse %s = %v, want nil", out, err)
	}
}

// A YAML file artemis would refuse to run is refused here, by the same loader
// `artemis parse` uses, and nothing is written.
func TestMigrateRefusesAScenarioArtemisCannotLoad(t *testing.T) {
	path := writeYAML(t, "broken.yaml", "name: n\ntype: functional\nsteps:\n  - name: s\n    respones: {}\n")
	out := filepath.Join(filepath.Dir(path), "broken.art")

	stdout, _, err := executeArgs(t, "migrate", "-f", path, "-o", out)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("%s was written for a file that does not load", out)
	}
}

// A construct with no DSL spelling names the step it was in, so the author of
// the suite knows which one to go and rewrite.
func TestMigrateRefusesWhatTheDSLCannotSay(t *testing.T) {
	path := writeYAML(t, "wild.yaml", `name: n
type: functional
steps:
  - name: "list items"
    type: api
    request:
      url: "u"
      method: "GET"
    response:
      status_code: 200
      body:
        - path: "$.items[*].sku"
          value: "a"
`)
	stdout, _, err := executeArgs(t, "migrate", "-f", path)
	if err == nil {
		t.Fatal("Execute() = nil, want an error")
	}
	for _, want := range []string{`step 1 "list items"`, "wildcard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// A comment with no home in the migrated file is dropped, and how many is said
// on stderr -- not on stdout, which carries the document.
func TestMigrateReportsDroppedComments(t *testing.T) {
	path := writeYAML(t, "commented.yaml", `name: n
type: functional
steps:
  - name: "ping"
    type: api
    request:
      # which host
      url: "u"
      method: "GET"
    response:
      status_code: 200
`)
	stdout, stderr, err := executeArgs(t, "migrate", "-f", path)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !strings.Contains(stderr, "1 comment") || !strings.Contains(stderr, "dropped") {
		t.Errorf("stderr = %q, want it to say one comment was dropped", stderr)
	}
	if strings.Contains(stdout, "which host") {
		t.Errorf("stdout holds the dropped comment:\n%s", stdout)
	}
}

// The two arguments that are the wrong kind of file. Saying so beats writing a
// file somebody has to diff to discover nothing happened.
func TestMigrateRefusesTheWrongExtensions(t *testing.T) {
	art := writeArt(t, "already.art", "scenario \"n\" {\n}\n")
	if _, _, err := executeArgs(t, "migrate", "-f", art); err == nil {
		t.Error("migrate -f already.art = nil error, want one")
	}

	yaml := writeYAML(t, "ping.yaml", smallYAML)
	if _, _, err := executeArgs(t, "migrate", "-f", yaml, "-o", "out.yaml"); err == nil {
		t.Error("migrate -o out.yaml = nil error, want one")
	}
}

// relation is how a fixture's migrated output stands to the .art file ART-38
// wrote by hand.
type relation int

const (
	// same: the bytes are equal. The hand-written file *is* a faithful
	// migration.
	same relation = iota

	// sameBelowComments: equal once each side's leading comment block is set
	// aside. ART-38 opened these files with paragraphs explaining a design
	// change -- "This fixture does *not* change by design, though the design
	// document says it does" -- which migration carries the YAML's own comment
	// instead of and cannot invent. Below the block they are the same file.
	sameBelowComments

	// diverges: the two files say different things, on purpose. The reason is
	// on the case.
	diverges

	// undecodable: the YAML does not load, so there is nothing to migrate and
	// the .art counterpart is a different mistake chosen to fail at the same
	// stage.
	undecodable
)

// corpusCase is one pkg/cli/testdata fixture and what is true of migrating it.
//
// This table is the issue's done-when, written out. It asks that every fixture
// migrate to a file that parses clean and that the output agree with ART-38's
// hand-written .art. The first half holds for all twelve that load. The second
// holds exactly as far as the hand-written files are faithful migrations, which
// is what each entry records: nothing is skipped silently, and every difference
// has its reason here rather than in a commit message.
type corpusCase struct {
	// name is the fixture's path under testdata, without its extension.
	name string

	rel relation

	// why is required for diverges and is printed when the case fails.
	why string

	// wantCodes are the diagnostic codes the migrated file produces, in order.
	// Empty for the ten that check clean; the two that do not are the design
	// moving a run-time failure to compile time, which is the point of the
	// rewrite rather than a migration bug.
	wantCodes []string
}

var corpus = []corpusCase{
	{name: "pass", rel: sameBelowComments},
	{name: "fail", rel: sameBelowComments},
	{name: "bad_capture", rel: sameBelowComments},
	{name: "retried", rel: sameBelowComments},
	{name: "regex_capture", rel: sameBelowComments},
	{name: "suite/01_login", rel: same},
	{name: "suite/nested/03_items", rel: same},
	{name: "report_json/01_health", rel: same},
	{name: "report_json/02_items", rel: same},

	// `delay: "soon"` is a run-time failure in YAML and a compile error in the
	// DSL -- pkg/dsl/testdata/invalid/bad_duration.art pins the same text --
	// so the migrated file is correct and does not check clean.
	{name: "bad_retry", rel: sameBelowComments, wantCodes: []string{"invalid-duration"}},

	// `{{tokn}}` names nothing, which the checker resolves. The migrated file
	// is the compile error the design says this fixture becomes.
	{name: "template_error", rel: sameBelowComments, wantCodes: []string{"unknown-identifier"}},

	{name: "wrong_status", rel: diverges, why: "wrong_status.art deliberately drops the YAML's " +
		"`$.status` body check, because the rule it pinned -- a wrong status suppresses the body " +
		"checks -- no longer exists, and status_and_body.art pins what replaced it. Migration " +
		"must carry that check, so the migrated file has one expect more"},

	{name: "suite/02_broken", rel: undecodable},
	{name: "report_json/03_broken", rel: undecodable},
}

// Every .yaml under testdata is in the table. A fixture added without a row
// would otherwise be migrated by nobody and prove nothing.
func TestMigrateCorpusCoversEveryFixture(t *testing.T) {
	listed := make(map[string]bool, len(corpus))
	for _, c := range corpus {
		listed[c.name] = true
	}

	found := 0
	err := filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !isYAMLFile(path) {
			return err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(path), "testdata/"), filepath.Ext(path))
		found++
		if !listed[name] {
			t.Errorf("testdata/%s.yaml has no row in corpus", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(corpus) {
		t.Errorf("walked %d YAML fixtures, the table holds %d", found, len(corpus))
	}
}

// The corpus itself: migrate each fixture, pin the output, check it, and hold
// it against the hand-written file.
//
// The name starts with TestGolden so that `make golden` regenerates these
// goldens along with every other set: the target runs `go test ./pkg/cli -run
// TestGolden -update`, and a golden nobody can regenerate from the Makefile is
// a golden that drifts.
func TestGoldenMigrations(t *testing.T) {
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			yamlPath := filepath.Join("testdata", filepath.FromSlash(c.name)+".yaml")

			if c.rel == undecodable {
				if _, _, err := executeArgs(t, "migrate", "-f", yamlPath); err == nil {
					t.Fatalf("migrate %s = nil error; the YAML does not load", yamlPath)
				}
				return
			}

			got, stderr, err := executeArgs(t, "migrate", "-f", yamlPath)
			if err != nil {
				t.Fatalf("migrate %s = %v\nstderr:\n%s", yamlPath, err, stderr)
			}
			checkMigrateGolden(t, c.name, got)
			assertCodes(t, c.name, got, c.wantCodes)
			assertAgreesWithHandWritten(t, c, got)
		})
	}
}

// checkMigrateGolden compares got with testdata/migrate/<name>.art, or rewrites
// it under -update. The golden is what makes the whole of a migration
// reviewable rather than only the lines a test happens to assert on.
func checkMigrateGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "migrate", filepath.FromSlash(name)+".art")

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatalf("%v\nrun `make golden` and read the diff", err)
	}
	if got != string(want) {
		t.Errorf("migrating %s.yaml gave\n%s\nthe golden holds\n%s", name, got, want)
	}
}

// assertCodes runs the front end over the migrated source and compares the
// diagnostic codes with the row's. A file expected to check clean that does not
// is a migration bug; a file expected to fail that passes means the design
// change the row records has been undone.
func assertCodes(t *testing.T, name, src string, want []string) {
	t.Helper()
	_, _, bag := frontEnd(name+".art", src)

	var got []string
	for _, d := range bag.All() {
		got = append(got, string(d.Code))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the migrated %s.art produced diagnostics %v, want %v\n%s", name, got, want, src)
	}
}

// assertAgreesWithHandWritten is the two-paths-must-agree half of the done-when.
//
// Both sides are canonically printed before they are compared, because
// migration's output always is and two of ART-38's files are not -- bad_retry's
// `retry` block is written across three lines where canonical mode fits it on
// one. Comparing the two formatters' idea of the same tree would be a test of
// nothing.
func assertAgreesWithHandWritten(t *testing.T, c corpusCase, got string) {
	t.Helper()
	path := filepath.Join("testdata", "art", filepath.FromSlash(c.name)+".art")
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	hand := reprint(t, path, string(raw))

	switch c.rel {
	case same:
		if got != string(raw) {
			t.Errorf("migrating %s.yaml does not reproduce %s byte for byte:\ngot\n%s\nwant\n%s",
				c.name, path, got, raw)
		}
	case sameBelowComments:
		gotBody, handBody := belowComments(got), belowComments(hand)
		if gotBody != handBody {
			t.Errorf("below its comment block, migrating %s.yaml does not reproduce %s:\ngot\n%s\nwant\n%s",
				c.name, path, gotBody, handBody)
		}
	case diverges:
		if belowComments(got) == belowComments(hand) {
			t.Errorf("%s.yaml now migrates to %s, so the row saying they differ is stale.\nThe reason it gave: %s",
				c.name, path, c.why)
		}
	}
}

// belowComments is src with its leading comment block removed: every line from
// the top that is blank or starts with a `#`, which in canonical layout is
// exactly the block above the first `scenario`.
func belowComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return strings.Join(lines[i:], "\n")
	}
	return ""
}

// reprint is src canonically printed, and is fmt_test.go's `canonical` with one
// difference: it insists only that the file *parse*, not that the checker
// accept it. Two fixtures in this corpus are files that parse clean and fail
// the checker by design, and they are exactly the two whose layout has to be
// normalised before a comparison.
func reprint(t *testing.T, path, src string) string {
	t.Helper()
	tree, bag := parser.Parse(path, src)
	if bag.HasErrors() {
		t.Fatalf("%s does not parse: %v", path, bag.All())
	}
	return print.Canonical(tree)
}

// The goldens are canonically formatted, which is the claim `artemis migrate`
// makes about every file it writes: running `artemis fmt` over one changes
// nothing.
func TestMigrateGoldensAreCanonical(t *testing.T) {
	for _, c := range corpus {
		if c.rel == undecodable {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join("testdata", "migrate", filepath.FromSlash(c.name)+".art")
			raw, err := os.ReadFile(path) //nolint:gosec // a path this test built
			if err != nil {
				t.Fatal(err)
			}
			if got := reprint(t, path, string(raw)); got != string(raw) {
				t.Errorf("%s is not canonically formatted; artemis fmt would write\n%s", path, got)
			}
		})
	}
}
