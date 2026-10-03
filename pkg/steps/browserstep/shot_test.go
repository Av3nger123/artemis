package browserstep

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failed step's screenshot goes where the run was told, under a name derived
// from the scenario and the step. No timestamp: a rerun overwrites the file
// from the run before it, which is what makes the path in the JSON report
// predictable enough for a CI job to name the artifact it is about to upload.
func TestAShotIsNamedForItsScenarioAndStep(t *testing.T) {
	dir := t.TempDir()
	f := newFake()
	s := NewShots(dir)

	path, err := s.On(&Bindings{driver: f}, "upgrade to pro", "check the receipt")
	if err != nil {
		t.Fatalf("On() = %v, want nil", err)
	}
	want := filepath.Join(dir, "upgrade-to-pro-check-the-receipt.png")
	if path != want {
		t.Errorf("On() = %q, want %q", path, want)
	}
	if len(f.shots) != 1 || f.shots[0] != want {
		t.Errorf("the driver was asked for %v, want one shot at %q", f.shots, want)
	}
}

// A name is slugged hard. A step is called "upgrade the plan (pro → max)" as
// often as not, and a path holding a slash, a quote or a colon is a path that
// breaks on some filesystem or in whatever reads the report.
func TestNamesAreSlugged(t *testing.T) {
	for _, tc := range []struct{ scenario, step, want string }{
		{"Upgrade To Pro", "Check", "upgrade-to-pro-check.png"},
		{"billing/v2", "GET /orders?id=1", "billing-v2-get-orders-id-1.png"},
		{`a "quoted" name`, "step: one", "a-quoted-name-step-one.png"},
		{"  spaced  ", "  out  ", "spaced-out.png"},
		{"upgrade (pro → max)", "x", "upgrade-pro-max-x.png"},
		// Letters of any script survive, so a suite written in another
		// language gets readable names rather than a row of hyphens.
		{"ログイン", "確認", "ログイン-確認.png"},
		// Nothing usable on either side still produces a file name.
		{"***", "///", "step.png"},
	} {
		dir := t.TempDir()
		got, err := NewShots(dir).On(&Bindings{driver: newFake()}, tc.scenario, tc.step)
		if err != nil {
			t.Fatalf("On(%q, %q) = %v", tc.scenario, tc.step, err)
		}
		if filepath.Base(got) != tc.want {
			t.Errorf("On(%q, %q) = %q, want %q", tc.scenario, tc.step, filepath.Base(got), tc.want)
		}
	}
}

// Two steps with the same name in one run do not overwrite each other, which a
// suite that writes `step "login"` in three scenarios of one file produces.
func TestTwoStepsWithOneNameGetTwoFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewShots(dir)

	var got []string
	for i := 0; i < 3; i++ {
		path, err := s.On(&Bindings{driver: newFake()}, "suite", "login")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.Base(path))
	}
	want := []string{"suite-login.png", "suite-login-2.png", "suite-login-3.png"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the names were %v, want %v", got, want)
	}
}

// The directory is made on the first write and not before: a suite of api
// steps, or a browser suite that passed, must not grow an empty folder in
// someone's working directory.
func TestTheDirectoryIsMadeLazily(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artemis-screenshots")
	s := NewShots(dir)

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the directory exists before anything failed: %v", err)
	}
	if _, err := s.On(&Bindings{driver: newFake()}, "s", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the directory was not made on the first write: %v", err)
	}
}

// An empty Dir is what `--screenshots ""` produces: the same call, the empty
// path back, and nothing written or created.
func TestAnEmptyDirTurnsScreenshotsOff(t *testing.T) {
	f := newFake()
	path, err := NewShots("").On(&Bindings{driver: f}, "s", "x")
	if err != nil {
		t.Fatalf("On() = %v, want nil -- off is not a failure", err)
	}
	if path != "" {
		t.Errorf("On() = %q, want the empty path", path)
	}
	if len(f.shots) != 0 {
		t.Errorf("the driver was asked for %v with screenshots off", f.shots)
	}
}

// A nil Shots and nil bindings are both "no screenshot" rather than a crash: a
// step with no page -- an api step the runner asked about anyway -- takes the
// same path.
func TestNilsAreNoScreenshotRatherThanACrash(t *testing.T) {
	var s *Shots
	if path, err := s.On(&Bindings{driver: newFake()}, "s", "x"); path != "" || err != nil {
		t.Errorf("(*Shots)(nil).On() = %q, %v; want \"\", nil", path, err)
	}
	if path, err := NewShots(t.TempDir()).On(nil, "s", "x"); path != "" || err != nil {
		t.Errorf("On(nil) = %q, %v; want \"\", nil", path, err)
	}
}

// A screenshot that could not be taken is an error with the path in it, for the
// caller to log. It must not be mistaken for "there is no screenshot": the
// difference between a failure artemis could not photograph and one it did not
// try to is worth a line in the log.
func TestAShotThatFailedIsReported(t *testing.T) {
	boom := errors.New("the page is closed")
	f := newFake().breaks("Screenshot", boom)

	path, err := NewShots(t.TempDir()).On(&Bindings{driver: f}, "s", "x")
	if err == nil {
		t.Fatal("On() = nil, want the driver's error")
	}
	if path != "" {
		t.Errorf("On() = %q beside an error, want the empty path", path)
	}
}

// A directory that cannot be made is reported with its path, because the usual
// cause is a permission or a name the filesystem will not take and neither is
// guessable from "screenshot failed".
func TestADirectoryThatCannotBeMadeIsReported(t *testing.T) {
	// A file where the directory should go: MkdirAll cannot make a directory
	// over it on any platform.
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewShots(blocked).On(&Bindings{driver: newFake()}, "s", "x")
	if err == nil {
		t.Fatal("On() = nil, want an error")
	}
	if !strings.Contains(err.Error(), blocked) {
		t.Errorf("On() = %v, want it to name %s", err, blocked)
	}
}

// The default is a folder in the working directory rather than a temporary one:
// the reader is either a person who wants to look at it or a CI job about to
// upload it, and neither is served by a path under /tmp.
func TestTheDefaultDirIsInTheWorkspace(t *testing.T) {
	if DefaultDir != "artemis-screenshots" {
		t.Errorf("DefaultDir = %q, want artemis-screenshots", DefaultDir)
	}
	if filepath.IsAbs(DefaultDir) {
		t.Errorf("DefaultDir = %q, want a relative path so it lands beside the suite", DefaultDir)
	}
}
