package browserstep

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// This file is the screenshot a failed browser step leaves behind: where it
// goes, what it is called, and the one rule that nothing is written unless
// something broke.
//
// It is here rather than in the runner because the naming is the part worth
// having in one place, and because a target other than a browser -- ART-48's
// generated Python -- will want to agree with it about what the file beside a
// failed step is called.

// DefaultDir is where a failed browser step's screenshot goes when nothing says
// otherwise.
//
// A folder in the working directory rather than a temporary one: the reader is
// either a person who wants to look at it or a CI job about to upload it as an
// artifact, and neither is served by a path under /tmp that the next step
// removes. It is created lazily -- on the first write -- so a suite of api
// steps never grows an empty folder.
const DefaultDir = "artemis-screenshots"

// Shots names and writes the screenshots of one run.
//
// It is one value per run rather than per step because the names have to be
// unique across the run, and because "has anything been written yet" is what
// decides whether the directory exists. A zero Shots writes nothing, which is
// what `--screenshots ""` produces: a caller that turned screenshots off calls
// the same method and gets the empty path back.
type Shots struct {
	// Dir is where the files go. Empty means screenshots are off.
	Dir string

	// used is the names already taken, so two steps with the same name in one
	// run do not overwrite each other. The value is how many have been seen,
	// which is what the -2, -3 suffix counts.
	used map[string]int
}

// NewShots returns a namer writing into dir. An empty dir turns screenshots
// off.
func NewShots(dir string) *Shots {
	return &Shots{Dir: dir, used: map[string]int{}}
}

// On records a screenshot of the page for a step that did not pass, and returns
// the path to put in the report.
//
// The empty string is not a failure: it is what "screenshots are off" and "this
// step has no page" both mean, and the caller writes it into the result tree as
// "no screenshot" either way.
//
// A screenshot that could not be written is reported as an error rather than
// swallowed, but it is the caller's business whether that outweighs the failure
// it was documenting -- see the runner, which logs it and keeps the step's own
// reason.
func (s *Shots) On(b *Bindings, scenario, step string) (string, error) {
	if s == nil || s.Dir == "" || b == nil {
		return "", nil
	}
	path := filepath.Join(s.Dir, s.name(scenario, step))
	// Lazily, and only now: a run with no failing browser step leaves no
	// directory behind.
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return "", fmt.Errorf("making the screenshot directory %s: %w", s.Dir, err)
	}
	if err := b.Screenshot(path); err != nil {
		return "", err
	}
	return path, nil
}

// name is the file name for one step's screenshot:
// `<scenario>-<step>.png`, slugged.
//
// Deterministic, with no timestamp and no counter in the common case. A rerun
// overwrites the file from the run before it, which is what a person comparing
// two runs wants and what keeps a workspace from filling up; a timestamp would
// make the path in the JSON report unpredictable, so nothing could assert on
// it and a CI job could not name the artifact it was about to upload.
//
// Uniqueness is still enforced, because two steps in one run may share a name:
// the second gets `-2`, the third `-3`. Within a run, not across runs.
func (s *Shots) name(scenario, step string) string {
	base := slug(scenario) + "-" + slug(step)
	if base == "-" {
		// Neither had a usable name, which a scenario whose file would not
		// load can produce. "step" is more use in a directory listing than "-".
		base = "step"
	}
	s.used[base]++
	if n := s.used[base]; n > 1 {
		base = fmt.Sprintf("%s-%d", base, n)
	}
	return base + ".png"
}

// slug turns a scenario or step name into a file name component: lower case,
// words joined by single hyphens, nothing else kept.
//
// Aggressive on purpose. A step is called "upgrade the plan (pro → max)" as
// often as not, and a path holding a slash, a quote or a colon is a path that
// breaks on some filesystem, in some shell, or in whatever reads the JSON
// report. Letters and digits of any script are kept -- unicode.IsLetter, not
// a-z -- so a suite written in another language gets readable names rather than
// a row of hyphens.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(unicode.ToLower(r))
		default:
			// Runs of anything else collapse to one separator, and a leading
			// or trailing run to nothing.
			dash = true
		}
	}
	return b.String()
}
