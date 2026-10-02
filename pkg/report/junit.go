package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"artemis/pkg/result"
)

// The JUnit report is the lingua franca of CI test reporting: Jenkins, GitLab,
// CircleCI and the GitHub Actions reporters all read it, and all of them group
// what they read by test suite. So:
//
//	a scenario is a <testsuite>
//	a step     is a <testcase>
//
// which is what makes "failures per scenario" fall out of a CI UI with no
// configuration. A step is the unit a person reruns and fixes, and the unit the
// console already prints with a duration and an attempt count; assertions live
// inside a case's <failure>, not as cases of their own.
//
// There is no official JUnit XML schema -- the format is the intersection of
// what Ant's emitter wrote and what each reporter parses -- so this writer emits
// only that intersection: name, tests, failures, errors, skipped, time,
// classname, file and timestamp. No system-out, no properties, nothing
// proprietary.
//
// The wire types below exist instead of `xml:` tags on pkg/result for the same
// reason the JSON report has its own: the document is a contract other programs
// parse, while the tree's field names are ours to rename.
// pkg/cli/testdata/report_junit.golden is a whole document to read.
type xmlTestSuites struct {
	XMLName   xml.Name       `xml:"testsuites"`
	Name      string         `xml:"name,attr"`
	Tests     int            `xml:"tests,attr"`
	Failures  int            `xml:"failures,attr"`
	Errors    int            `xml:"errors,attr"`
	Skipped   int            `xml:"skipped,attr"`
	Time      string         `xml:"time,attr"`
	Timestamp string         `xml:"timestamp,attr,omitempty"`
	Suites    []xmlTestSuite `xml:"testsuite"`
}

// xmlTestSuite is one scenario. It carries no timestamp: the result tree records
// no per-scenario start time, and stamping each suite with the run's start would
// be a plausible-looking lie.
type xmlTestSuite struct {
	Name     string        `xml:"name,attr"`
	File     string        `xml:"file,attr,omitempty"`
	Tests    int           `xml:"tests,attr"`
	Failures int           `xml:"failures,attr"`
	Errors   int           `xml:"errors,attr"`
	Skipped  int           `xml:"skipped,attr"`
	Time     string        `xml:"time,attr"`
	Cases    []xmlTestCase `xml:"testcase"`
}

// xmlTestCase is one step. Classname is the scenario's file path rather than its
// name: a reporter keys a case on classname plus name and displays them joined,
// so the path makes a case unique across a suite and points at the file to edit.
// The scenario's name is right there on the enclosing <testsuite>.
//
// At most one of Failure, Error and Skipped is set -- a step has one status.
type xmlTestCase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	Time      string     `xml:"time,attr"`
	Failure   *xmlResult `xml:"failure,omitempty"`
	Error     *xmlResult `xml:"error,omitempty"`
	Skipped   *xmlResult `xml:"skipped,omitempty"`
}

// xmlResult is a <failure>, an <error> or a <skipped>. Message is the one line a
// CI UI shows in its list of tests; the character data is what someone reads
// when they open the case.
type xmlResult struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// junitSuiteName is the <testsuites> name. A reporter shows it as the name of
// the whole report.
const junitSuiteName = "artemis"

// loadFailureCase is the name of the case a scenario gets when its file would
// not load. Without a case the load failure is invisible in a CI UI: an empty
// suite is rendered as nothing much, however high its errors attribute.
const loadFailureCase = "could not load"

// WriteJUnit writes run to w as one JUnit XML document, indented, with the XML
// declaration and a trailing newline.
//
// A nil run is an empty run, not a crash: a caller that asked for a report
// deserves a document saying nothing ran.
//
// Any byte XML 1.0 forbids -- a control character out of a response body or an
// exec step's output -- is replaced with U+FFFD by encoding/xml, so the document
// parses whatever a server sent.
func WriteJUnit(w io.Writer, run *result.RunResult) error {
	raw, err := xml.MarshalIndent(junitFromRun(run), "", "  ")
	if err != nil {
		// Unreachable: every field of the wire types is a string, an int or a
		// slice of them.
		return fmt.Errorf("encoding the JUnit report: %w", err)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("writing the JUnit report: %w", err)
	}
	if _, err := w.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("writing the JUnit report: %w", err)
	}
	return nil
}

// junitFromRun maps the result tree onto the wire types.
//
// The tallies are counted from the cases actually emitted rather than taken from
// run.Counts(): a scenario that would not load contributes a case with no step
// behind it, and a reporter adds up cases.
func junitFromRun(run *result.RunResult) xmlTestSuites {
	if run == nil {
		run = &result.RunResult{}
	}
	out := xmlTestSuites{
		Name:      junitSuiteName,
		Time:      seconds(run.Duration),
		Timestamp: rfc3339(run.StartedAt),
		Suites:    make([]xmlTestSuite, 0, len(run.Scenarios)),
	}
	for _, sc := range run.Scenarios {
		if sc == nil {
			continue
		}
		suite := junitSuite(sc)
		out.Tests += suite.Tests
		out.Failures += suite.Failures
		out.Errors += suite.Errors
		out.Skipped += suite.Skipped
		out.Suites = append(out.Suites, suite)
	}
	return out
}

func junitSuite(sc *result.ScenarioResult) xmlTestSuite {
	name := sc.Name
	if name == "" {
		// A scenario whose file would not load has no name, so the path is all
		// anyone knows about it.
		name = sc.File
	}
	classname := sc.File
	if classname == "" {
		classname = name
	}

	out := xmlTestSuite{
		Name:  name,
		File:  sc.File,
		Time:  seconds(sc.Duration),
		Cases: make([]xmlTestCase, 0, len(sc.Steps)),
	}
	for _, step := range sc.Steps {
		if step == nil {
			continue
		}
		out.add(junitCase(step, classname))
	}
	if len(out.Cases) == 0 && !sc.Passed() {
		out.add(xmlTestCase{
			Name:      loadFailureCase,
			Classname: classname,
			Time:      seconds(sc.Duration),
			Error:     &xmlResult{Message: sc.Error, Text: sc.Error},
		})
	}
	return out
}

// add appends a case and counts it.
func (s *xmlTestSuite) add(c xmlTestCase) {
	s.Tests++
	switch {
	case c.Failure != nil:
		s.Failures++
	case c.Error != nil:
		s.Errors++
	case c.Skipped != nil:
		s.Skipped++
	}
	s.Cases = append(s.Cases, c)
}

func junitCase(step *result.StepResult, classname string) xmlTestCase {
	out := xmlTestCase{
		Name:      step.Name,
		Classname: classname,
		Time:      seconds(step.Duration),
	}
	switch step.Status {
	case result.StatusFail:
		out.Failure = junitResult(junitReasons(step), "the step failed")
	case result.StatusError:
		out.Error = junitResult(junitReasons(step), "the step could not run")
	case result.StatusSkip:
		// A skip's reason is the only thing to say about it, and a reporter
		// shows it from the attribute.
		out.Skipped = &xmlResult{Message: step.Error}
	}
	return out
}

// junitReasons is every line worth saying about a step that did not pass: the
// step's own error, then each assertion that did not pass, described the same way
// the console and the run's exit error describe it -- three outputs that must not
// drift.
func junitReasons(step *result.StepResult) []string {
	var lines []string
	if step.Error != "" {
		lines = append(lines, step.Error)
	}
	for _, a := range step.Assertions {
		if !a.Passed() {
			lines = append(lines, a.Describe())
		}
	}
	return lines
}

// junitResult packs the reasons into one element: the first line becomes the
// message, suffixed with how many more there are, and every line becomes the
// text.
//
// One element, not one per reason: several reporters render only the first
// <failure> child of a case and a few reject more than one, so an element per
// assertion would lose assertions in exactly the tools this report exists for.
func junitResult(lines []string, fallback string) *xmlResult {
	if len(lines) == 0 {
		// Reachable only from a status the tree set without a reason.
		lines = []string{fallback}
	}
	message := lines[0]
	if more := len(lines) - 1; more > 0 {
		message += fmt.Sprintf(" (+%d more)", more)
	}
	return &xmlResult{Message: message, Text: strings.Join(lines, "\n")}
}

// seconds renders a duration the way a JUnit reporter parses the time
// attribute: seconds, to milliseconds. The JSON report's duration_ms would read
// as a 1000x regression here.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Round(time.Millisecond).Seconds(), 'f', 3, 64)
}
