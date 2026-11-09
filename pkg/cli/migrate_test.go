package cli

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
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

// -o names a folder, and a YAML scenario becomes a flow and a scenario file in
// it, laid out as an old .art file's migration is: the paths written on
// stdout, nothing on stderr, and the YAML file left exactly as it was.
// Migration never deletes its input.
func TestMigrateWritesAFlowAndAScenarioFile(t *testing.T) {
	path := writeYAML(t, "ping.yaml", smallYAML)
	out := filepath.Join(t.TempDir(), "new")

	stdout, stderr, err := executeArgs(t, "migrate", "-f", path, "-o", out)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil\nstderr:\n%s", err, stderr)
	}
	flow := filepath.Join(out, "flows", "ping.art")
	scenario := filepath.Join(out, "scenarios", "ping.art")
	if want := flow + "\n" + scenario + "\n"; stdout != want {
		t.Errorf("stdout = %q, want the two paths written, %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	wantFlow := `# what this file is for
flow ping {
  # what this step does
  step ping {
    get "%SERVER%/ping"
    expect status == 200
  }
}
`
	if got := readFile(t, flow); got != wantFlow {
		t.Errorf("%s =\n%s\nwant\n%s", flow, got, wantFlow)
	}
	wantScenario := "flow \"../flows/ping.art\"\n\nscenario ping {}\n"
	if got := readFile(t, scenario); got != wantScenario {
		t.Errorf("%s =\n%s\nwant\n%s", scenario, got, wantScenario)
	}

	// And the scenario file is one the front end accepts, which is the whole
	// promise of the command.
	if _, _, err := executeArgs(t, "parse", "-f", scenario); err != nil {
		t.Errorf("parse %s = %v, want nil", scenario, err)
	}
	if got := readFile(t, path); got != smallYAML {
		t.Errorf("the YAML file changed:\n%s", got)
	}
}

// Two files need a folder: without -o, or with -o naming a file, nothing is
// written and the error says what -o takes.
func TestMigrateWritesTheFileWithOut(t *testing.T) {
	path := writeYAML(t, "ping.yaml", smallYAML)
	dir := filepath.Dir(path)

	stdout, _, err := executeArgs(t, "migrate", "-f", path)
	if err == nil || !strings.Contains(err.Error(), "-o <dir>") {
		t.Errorf("migrate -f ping.yaml = %v, want an error asking for -o <dir>", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	for _, out := range []string{filepath.Join(dir, "ping.art"), filepath.Join(dir, "out.yaml")} {
		if _, _, err := executeArgs(t, "migrate", "-f", path, "-o", out); err == nil || !strings.Contains(err.Error(), "is a file") {
			t.Errorf("migrate -o %s = %v, want an error saying -o names a folder", out, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("a refused migration wrote beside the YAML: %v", entries)
	}
}

// A YAML file artemis would refuse to run is refused here, by the same loader
// `artemis parse` uses, and nothing is written.
func TestMigrateRefusesAScenarioArtemisCannotLoad(t *testing.T) {
	path := writeYAML(t, "broken.yaml", "name: n\ntype: functional\nsteps:\n  - name: s\n    respones: {}\n")
	out := filepath.Join(t.TempDir(), "new")

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
	stdout, _, err := executeArgs(t, "migrate", "-f", path, "-o", filepath.Join(t.TempDir(), "new"))
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
// on stderr.
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
	out := filepath.Join(t.TempDir(), "new")
	_, stderr, err := executeArgs(t, "migrate", "-f", path, "-o", out)
	if err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if !strings.Contains(stderr, "1 comment") || !strings.Contains(stderr, "dropped") {
		t.Errorf("stderr = %q, want it to say one comment was dropped", stderr)
	}
	if flow := readFile(t, filepath.Join(out, "flows", "commented.art")); strings.Contains(flow, "which host") {
		t.Errorf("the flow holds the dropped comment:\n%s", flow)
	}
}

// The arguments that cannot work for a .art file: it becomes several files, so
// it needs a folder to write them in.
func TestMigrateRefusesTheWrongExtensions(t *testing.T) {
	art := writeArt(t, "already.art", "scenario \"n\" {\n}\n")
	if _, _, err := executeArgs(t, "migrate", "-f", art); err == nil || !strings.Contains(err.Error(), "-o <dir>") {
		t.Errorf("migrate -f already.art = %v, want an error asking for -o <dir>", err)
	}
	file := writeArt(t, "taken.art", "")
	if _, _, err := executeArgs(t, "migrate", "-f", art, "-o", file); err == nil {
		t.Error("migrate -f already.art -o taken.art = nil error, want one: -o names a folder")
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

// The corpus itself: migrate each fixture, pin the two files it becomes, check
// them, and hold them against the hand-written pair.
//
// The name starts with TestGolden so that `make golden` regenerates these
// goldens along with every other set: the target runs `go test ./pkg/cli -run
// TestGolden -update`, and a golden nobody can regenerate from the Makefile is
// a golden that drifts.
func TestGoldenMigrations(t *testing.T) {
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			yamlPath := filepath.Join("testdata", filepath.FromSlash(c.name)+".yaml")
			out := t.TempDir()

			if c.rel == undecodable {
				if _, _, err := executeArgs(t, "migrate", "-f", yamlPath, "-o", out); err == nil {
					t.Fatalf("migrate %s = nil error; the YAML does not load", yamlPath)
				}
				return
			}

			if _, stderr, err := executeArgs(t, "migrate", "-f", yamlPath, "-o", out); err != nil {
				t.Fatalf("migrate %s = %v\nstderr:\n%s", yamlPath, err, stderr)
			}
			pair := migrated(t, c.name, out)
			for _, key := range []string{pair.flowKey, pair.scenarioKey} {
				checkMigrateGolden(t, migrateGolden(c.name, key), pair.files[key])
			}
			assertCodes(t, c.name, pair, c.wantCodes)
			assertAgreesWithHandWritten(t, c, pair)
		})
	}
}

// migrateGolden is where the golden of one migrated file is: testdata/migrate,
// then the fixture's folder, then the file's path under -o -- so the goldens of
// suite/01_login.yaml are testdata/migrate/suite/flows/01_login.art and
// testdata/migrate/suite/scenarios/01_login.art, and each scenario file's
// header names the flow beside it.
func migrateGolden(name, key string) string {
	return filepath.Join("testdata", "migrate", filepath.FromSlash(path.Dir(name)), filepath.FromSlash(key))
}

// checkMigrateGolden compares got with the golden at path, or rewrites it
// under -update. The golden is what makes the whole of a migration reviewable
// rather than only the lines a test happens to assert on.
func checkMigrateGolden(t *testing.T, path, got string) {
	t.Helper()
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
		t.Errorf("migrating gave\n%s\nthe golden %s holds\n%s", got, path, want)
	}
}

// migratedPair is the flow and the scenario file `artemis migrate -f x.yaml -o
// <dir>` wrote for one YAML fixture.
type migratedPair struct {
	files                map[string]string // both, by the keys below
	flowKey, scenarioKey string
}

// migrated reads the pair the fixture called name was migrated to under out:
// flows/<base>.art and scenarios/<base>.art, whatever folder the fixture is
// in, and nothing else.
func migrated(t *testing.T, name, out string) migratedPair {
	t.Helper()
	base := path.Base(name) + artExt
	p := migratedPair{files: map[string]string{}, flowKey: "flows/" + base, scenarioKey: "scenarios/" + base}
	for _, key := range []string{p.flowKey, p.scenarioKey} {
		p.files[key] = readFile(t, filepath.Join(out, filepath.FromSlash(key)))
	}
	written := 0
	err := filepath.WalkDir(out, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			written++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if written != len(p.files) {
		t.Errorf("migrating %s.yaml wrote %d files, want the flow and the scenario file", name, written)
	}
	return p
}

// assertCodes runs the front end over the migrated scenario file and compares
// the diagnostic codes with the row's. A file expected to check clean that does
// not is a migration bug; a file expected to fail that passes means the design
// change the row records has been undone.
func assertCodes(t *testing.T, name string, p migratedPair, want []string) {
	t.Helper()
	bag := front.CompileWith(p.scenarioKey, p.files[p.scenarioKey], expand.MapLoader(p.files)).Bag

	var got []string
	for _, d := range bag.All() {
		got = append(got, string(d.Code))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the migrated %s.art produced diagnostics %v, want %v\n%s", name, got, want, p.files[p.flowKey])
	}
}

// handWritten is where testdata/art holds the fixture called name: its flow in
// flows/ and its scenario file in scenarios/, under the corpus folder the
// fixture belongs to -- report_json/, suite/ -- or under testdata/art itself,
// with any folder below that kept beneath both.
func handWritten(name string) (flow, scenario string) {
	root, rest := "", name
	if i := strings.Index(name, "/"); i >= 0 {
		root, rest = name[:i], name[i+1:]
	}
	at := func(kind string) string {
		return filepath.Join("testdata", artDir, root, kind, filepath.FromSlash(rest)+artExt)
	}
	return at("flows"), at("scenarios")
}

// belowHeader is a scenario file without its first line, the header naming its
// flow, which is a path relative to where the file is.
func belowHeader(src string) string {
	_, rest, _ := strings.Cut(src, "\n")
	return rest
}

// assertAgreesWithHandWritten is the two-paths-must-agree half of the done-when:
// the YAML fixture, migrated and carried on into the new forms, against the
// flow and scenario file testdata/art holds.
//
// The hand-written flow is canonically printed before it is compared, because
// migration's output always is. Comparing the two formatters' idea of the same
// tree would be a test of nothing.
func assertAgreesWithHandWritten(t *testing.T, c corpusCase, p migratedPair) {
	t.Helper()
	flowPath, scenarioPath := handWritten(c.name)
	raw, err := os.ReadFile(flowPath) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	hand := reprint(t, flowPath, string(raw))
	got := p.files[p.flowKey]

	scenario, err := os.ReadFile(scenarioPath) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	if c.rel != diverges && belowHeader(p.files[p.scenarioKey]) != belowHeader(string(scenario)) {
		t.Errorf("below its header, migrating %s.yaml does not reproduce %s:\ngot\n%s\nwant\n%s",
			c.name, scenarioPath, p.files[p.scenarioKey], scenario)
	}

	switch c.rel {
	case same:
		if got != string(raw) {
			t.Errorf("migrating %s.yaml does not reproduce %s byte for byte:\ngot\n%s\nwant\n%s",
				c.name, flowPath, got, raw)
		}
	case sameBelowComments:
		gotBody, handBody := belowComments(got), belowComments(hand)
		if gotBody != handBody {
			t.Errorf("below its comment block, migrating %s.yaml does not reproduce %s:\ngot\n%s\nwant\n%s",
				c.name, flowPath, gotBody, handBody)
		}
	case diverges:
		if belowComments(got) == belowComments(hand) {
			t.Errorf("%s.yaml now migrates to %s, so the row saying they differ is stale.\nThe reason it gave: %s",
				c.name, flowPath, c.why)
		}
	}
}

// belowComments is src with its leading comment block removed: every line from
// the top that is blank or starts with a `#`, which in canonical layout is
// exactly the block above the first `flow` or `scenario`.
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
			base := path.Base(c.name) + artExt
			for _, key := range []string{"flows/" + base, "scenarios/" + base} {
				golden := migrateGolden(c.name, key)
				raw := readFile(t, golden)
				if got := reprint(t, golden, raw); got != raw {
					t.Errorf("%s is not canonically formatted; artemis fmt would write\n%s", golden, got)
				}
			}
		})
	}
}
