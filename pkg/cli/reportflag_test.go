package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/result"
)

// The grammar, case by case: a bare format is stdout, `=-` is stdout said out
// loud, anything else is a file, and the order the user gave is kept.
func TestParseReportsReadsTheGrammar(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []reportTarget
	}{
		{name: "none"},
		{
			name: "bare format is stdout",
			args: []string{"json"},
			want: []reportTarget{{format: "json"}},
		},
		{
			name: "a dash is stdout said out loud",
			args: []string{"json=-"},
			want: []reportTarget{{format: "json", path: "-"}},
		},
		{
			name: "a path is a file",
			args: []string{"json=results.json"},
			want: []reportTarget{{format: "json", path: "results.json"}},
		},
		{
			name: "a path may contain an equals sign",
			args: []string{"json=out/run=1.json"},
			want: []reportTarget{{format: "json", path: "out/run=1.json"}},
		},
		{
			name: "surrounding space in the format is ignored",
			args: []string{" json "},
			want: []reportTarget{{format: "json"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseReports(tc.args)
			if err != nil {
				t.Fatalf("parseReports(%q) = %v, want nil", tc.args, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseReports(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

// Every refusal names what was wrong. These are checked before a scenario runs,
// so the message is all the user gets.
func TestParseReportsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "unknown format",
			args: []string{"yaml"},
			want: []string{"yaml", "json"},
		},
		{
			name: "no format at all",
			args: []string{"=results.json"},
			want: []string{"no format given", "json"},
		},
		{
			name: "the same format twice",
			args: []string{"json=a.json", "json=b.json"},
			want: []string{"json", "twice"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseReports(tc.args)
			if err == nil {
				t.Fatalf("parseReports(%q) = nil, want an error", tc.args)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("parseReports(%q) = %q, want it to mention %q", tc.args, err, want)
				}
			}
		})
	}
}

// Two documents on one stream is unparseable, and it is exactly what the flag
// exists to prevent, so it is refused rather than interleaved. Reaching it needs
// two formats, which json alone is not, so a second one is registered for the
// length of this test -- the case has to be pinned before ART-11 adds junit, not
// after.
func TestParseReportsRefusesTwoReportsOnStdout(t *testing.T) {
	withFormat(t, "testfmt")

	_, err := parseReports([]string{"json", "testfmt=-"})
	if err == nil {
		t.Fatal("parseReports() = nil, want an error for two reports on stdout")
	}
	for _, want := range []string{"json", "testfmt", "stdout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("parseReports() = %q, want it to mention %q", err, want)
		}
	}
}

// Two formats are fine as long as only one of them takes stdout.
func TestParseReportsAllowsTwoFormatsWhenOnlyOneTakesStdout(t *testing.T) {
	withFormat(t, "testfmt")

	got, err := parseReports([]string{"json", "testfmt=out.txt"})
	if err != nil {
		t.Fatalf("parseReports() = %v, want nil", err)
	}
	want := []reportTarget{{format: "json"}, {format: "testfmt", path: "out.txt"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseReports() = %+v, want %+v", got, want)
	}
}

// withFormat registers a second report format for the length of a test, so the
// two-format cases can be pinned while json is the only real one.
func withFormat(t *testing.T, format string) {
	t.Helper()
	if _, taken := reportWriters[format]; taken {
		t.Fatalf("%q is a real format; pick a name that is not", format)
	}
	reportWriters[format] = func(w io.Writer, _ *result.RunResult) error {
		_, err := io.WriteString(w, format+"\n")
		return err
	}
	t.Cleanup(func() { delete(reportWriters, format) })
}

func TestAnyToStdoutIsFalseWhenEveryReportIsAFile(t *testing.T) {
	targets, err := parseReports([]string{"json=results.json"})
	if err != nil {
		t.Fatalf("parseReports() = %v, want nil", err)
	}
	if anyToStdout(targets) {
		t.Error("anyToStdout() = true, want false when every report is a file")
	}
}

// writeReports sends a stdout target to the writer it was given, so the caller
// decides what stdout is -- which is what lets a test capture it.
func TestWriteReportsWritesStdoutTargetsToTheGivenWriter(t *testing.T) {
	var buf bytes.Buffer
	run := &result.RunResult{Status: result.StatusPass}
	if err := writeReports(&buf, []reportTarget{{format: "json"}}, run); err != nil {
		t.Fatalf("writeReports() = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), `"schema_version"`) {
		t.Errorf("writeReports() wrote %q, want a JSON document", buf.String())
	}
}

// A path that already holds the last run's report is truncated, not appended to:
// two documents in one file is a file nothing can parse.
func TestWriteReportsTruncatesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}

	run := &result.RunResult{Status: result.StatusPass}
	if err := writeReports(&bytes.Buffer{}, []reportTarget{{format: "json", path: path}}, run); err != nil {
		t.Fatalf("writeReports() = %v, want nil", err)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // a path this test just wrote
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "x") {
		t.Errorf("the file still holds the previous run's bytes:\n%s", raw)
	}
	if !strings.HasPrefix(string(raw), "{") {
		t.Errorf("the file does not start with a document:\n%s", raw)
	}
}

// A report that cannot be written is an error naming the format and the path. A
// CI job whose artifact silently vanished is worse off than one that went red.
func TestWriteReportsFailsOnAnUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "results.json")
	err := writeReports(&bytes.Buffer{}, []reportTarget{{format: "json", path: path}}, &result.RunResult{})
	if err == nil {
		t.Fatal("writeReports() = nil, want an error")
	}
	for _, want := range []string{"json", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("writeReports() = %q, want it to mention %q", err, want)
		}
	}
}
