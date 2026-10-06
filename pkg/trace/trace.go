// Package trace writes what a step sent and what it saw, as a file beside the
// run. ART-55.
//
// # Why this is not in the report
//
// A report is a verdict: what failed, where, and what it compared. A trace is
// evidence, it is large, and most of it is about steps that passed. Inlining a
// trace of a two-hundred-step suite into report.json would make the document
// nobody could open the only document there is. So a trace is a file per step
// and the report carries its path, which is exactly what a screenshot already
// does -- see result.StepResult.Screenshot and pkg/steps/browserstep's Shots,
// whose naming rules this file follows deliberately.
//
// # Why the executor did not have to change
//
// Everything a trace needs is already in the runner's hands at the moment an
// attempt finishes: models.Step is what the step sent, and the roots map an
// Executor returned is what it saw. So executor.Executor keeps its one method,
// and none of its four implementations know this package exists.
//
// # Why the shape is the roots map
//
// The observation is already a map[string]any keyed by the names a step type
// binds -- status, body, raw, headers for an api step; exit_code, stdout, stderr
// for a terminal one. Writing that map as it stands means one trace format for
// every step type, and a fifth step type gets a trace without this package
// being touched.
//
// # What it withholds
//
// Both halves of models.Secrets. The request marks say which header, body field
// or environment setting came from a `secret` binding. The observation marks say
// where a secret capture read its value from, which is the half a reader of
// ART-54 alone would miss: a `secret capture token = body.data.access_token`
// withholds every later use of the token, and the token is still sitting in this
// step's response body.
package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"artemis/pkg/shared/models"
)

// DefaultDir is where traces go when the flag names no directory.
//
// A folder in the working directory rather than under /tmp, for the reason
// browserstep.DefaultDir gives: the reader is a person about to look at it or a
// CI job about to upload it, and neither is served by a path the next step
// removes. It is created on the first write, so a run with tracing off -- or one
// that never ran a step -- leaves no empty folder.
const DefaultDir = "artemis-traces"

// MaxBytes is how much of one string value a trace keeps.
//
// A trace that exhausted memory on a step that downloaded a file would be worse
// than no trace, so there is a limit, and it is visible in the file rather than
// silent: a truncated value is marked and Record.Truncated names what was cut.
const MaxBytes = 64 << 10

// Redacted is what stands in for a withheld value. It is result's spelling, so
// a reader who has seen a report recognises it here.
const Redacted = "***"

// Record is one step's trace: what it sent, what it saw, and what was withheld.
type Record struct {
	// Scenario and Step name where this came from, so a file stands alone. A
	// reader who found it in a directory should not have to consult the report
	// to learn what it is about.
	Scenario string `json:"scenario"`
	Step     string `json:"step"`

	// Type is the step type: api, terminal, browser.
	Type string `json:"type"`

	// Attempt is which attempt this records, and it is always the last one.
	// pkg/executor's contract is that "a later attempt replaces an earlier one
	// whole, so a recorded step is never a mix of two tries"; a trace of every
	// attempt would contradict the result tree beside it.
	Attempt int `json:"attempt"`

	// Sent is what the step did, in the shape its type gives it.
	Sent map[string]any `json:"sent"`

	// Observed is what the step saw: the roots map, withheld where it must be.
	// Absent for a step that never got an observation at all.
	Observed map[string]any `json:"observed,omitempty"`

	// Error is why the step could not run, for an attempt that produced no
	// observation. A trace is most useful exactly here, where there is no
	// response to read.
	Error string `json:"error,omitempty"`

	// Truncated names the fields MaxBytes cut, so a reader knows the difference
	// between a short value and a clipped one.
	Truncated []string `json:"truncated,omitempty"`
}

// Writer names and writes the traces of one run.
//
// One value per run, because the names have to be unique across it and because
// "has anything been written" is what decides whether the directory exists. A
// nil Writer and an empty Dir both write nothing and return the empty path,
// so a caller with tracing off makes the same call.
type Writer struct {
	// Dir is where the files go. Empty means tracing is off.
	Dir string

	// mu guards used and the write, because `--jobs` runs scenario files at the
	// same time. Held across the whole of On, for the reason browserstep.Shots
	// holds its own: the work is a file write, so overlapping two buys nothing
	// and serialising them keeps the -2 suffix honest.
	mu sync.Mutex

	// used counts the names already taken, so two steps with one name in a run
	// do not overwrite each other.
	used map[string]int
}

// New returns a Writer for dir. An empty dir turns tracing off.
func New(dir string) *Writer {
	return &Writer{Dir: dir, used: map[string]int{}}
}

// On writes one step's trace and returns the path to record in the report.
//
// The empty string is not a failure: it is what "tracing is off" means, and the
// caller writes it into the result tree as "no trace".
func (w *Writer) On(rec Record) (string, error) {
	if w == nil || w.Dir == "" {
		return "", nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	path := filepath.Join(w.Dir, w.name(rec.Scenario, rec.Step))
	if err := os.MkdirAll(w.Dir, 0o750); err != nil {
		return "", fmt.Errorf("making the trace directory %s: %w", w.Dir, err)
	}
	doc, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding the trace for step %q: %w", rec.Step, err)
	}
	// 0o600: a trace holds whatever the service answered, and the withholding
	// above is best-effort over what the scenario declared.
	if err := os.WriteFile(path, append(doc, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("writing the trace %s: %w", path, err)
	}
	return path, nil
}

// name is `<scenario>-<step>.json`, slugged, with the same rules as a
// screenshot's: deterministic so a rerun overwrites and a CI job can predict
// the path, and counted so two steps sharing a name do not collide.
func (w *Writer) name(scenario, step string) string {
	base := slug(scenario) + "-" + slug(step)
	if base == "-" {
		base = "step"
	}
	w.used[base]++
	if n := w.used[base]; n > 1 {
		base = fmt.Sprintf("%s-%d", base, n)
	}
	return base + ".json"
}

// slug turns a name into a file name component: lower case, words joined by
// single hyphens, nothing else kept. It is browserstep's rule, for the reason
// that file gives -- a step is called "upgrade the plan (pro -> max)" as often
// as not -- and the two must agree so the trace and the screenshot of one step
// sit under the same name.
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
			dash = true
		}
	}
	return b.String()
}

// Of builds the record for one attempt.
//
// roots is what the executor returned, and nil for an attempt that failed before
// it saw anything -- in which case err says why, which is the case a trace helps
// with most.
// secrets are the resolved values of the step's secret bindings, scrubbed from
// the whole record after the marks have been applied. See scrub.
func Of(scenario string, step models.Step, attempt int, roots map[string]any, err error, secrets ...string) Record {
	rec := Record{
		Scenario: scenario,
		Step:     step.Name,
		Type:     step.Type,
		Attempt:  attempt,
		Sent:     sent(step),
	}
	if err != nil {
		rec.Error = err.Error()
	}
	var cut []string
	rec.Sent, cut = clamp(rec.Sent, "sent")
	rec.Truncated = append(rec.Truncated, cut...)
	if roots != nil {
		observed, cutObserved := clamp(withheldRoots(roots, step.Secrets), "observed")
		rec.Observed = observed
		rec.Truncated = append(rec.Truncated, cutObserved...)
	}
	// Last, over everything the marks have already been applied to: the marks
	// are precise about the places this scenario named, and this is the net for
	// the places only the run knows about.
	return scrub(rec, secrets)
}
