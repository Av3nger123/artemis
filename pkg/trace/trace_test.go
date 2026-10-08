package trace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artemis/pkg/shared/models"
)

func apiStep(secrets models.Secrets) models.Step {
	return models.Step{
		Name: "get a token",
		Type: "api",
		Request: models.Request{
			Method: "POST",
			URL:    "https://api.test/token",
			Headers: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer hunter2",
			},
			Body: `{"username":"alice","password":"hunter2"}`,
		},
		Secrets: secrets,
	}
}

// The request half: a marked header is withheld and an unmarked one is not.
func TestSentWithholdsAMarkedHeader(t *testing.T) {
	rec := Of("checkout", apiStep(models.Secrets{
		Headers: map[string]bool{"Authorization": true},
	}), 1, nil, nil)

	headers := rec.Sent["headers"].(map[string]any)
	if headers["Authorization"] != Redacted {
		t.Errorf("Authorization = %v, want withheld", headers["Authorization"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %v, want it kept", headers["Content-Type"])
	}
}

// A body is withheld field by field, so the readable fields stay readable.
func TestSentWithholdsOneBodyField(t *testing.T) {
	rec := Of("checkout", apiStep(models.Secrets{
		BodyPaths: []string{"/password"},
	}), 1, nil, nil)

	body := rec.Sent["body"].(map[string]any)
	if body["password"] != Redacted {
		t.Errorf("password = %v, want withheld", body["password"])
	}
	if body["username"] != "alice" {
		t.Errorf("username = %v, want it kept", body["username"])
	}
}

func TestSentWithholdsAWholeBody(t *testing.T) {
	rec := Of("checkout", apiStep(models.Secrets{Body: true}), 1, nil, nil)
	if rec.Sent["body"] != Redacted {
		t.Errorf("body = %v, want withheld whole", rec.Sent["body"])
	}
}

// The leak this half of the feature exists to stop: the credential's origin is
// the response of the step that captured it, so marking the binding is not
// enough on its own.
func TestObservedWithholdsTheCapturedPath(t *testing.T) {
	roots := map[string]any{
		"status": float64(200),
		"body": map[string]any{
			"data": map[string]any{"access_token": "t0ken-abc", "expires_in": float64(3600)},
		},
		"raw": `{"data":{"access_token":"t0ken-abc","expires_in":3600}}`,
	}
	rec := Of("checkout", apiStep(models.Secrets{
		ObservedPaths: map[string][]string{"body": {"/data/access_token"}},
	}), 1, roots, nil)

	doc, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "t0ken-abc") {
		t.Errorf("the token reached the trace:\n%s", doc)
	}
	data := rec.Observed["body"].(map[string]any)["data"].(map[string]any)
	if data["access_token"] != Redacted {
		t.Errorf("access_token = %v, want withheld", data["access_token"])
	}
	if data["expires_in"] != float64(3600) {
		t.Errorf("expires_in = %v, want it kept: the rest of the response is the point", data["expires_in"])
	}
	if rec.Observed["status"] != float64(200) {
		t.Errorf("status = %v, want it kept", rec.Observed["status"])
	}
}

// `raw` and `body` are the same bytes read two ways. Withholding one and
// printing the other would withhold nothing at all.
func TestObservedWithholdsRawAlongsideBody(t *testing.T) {
	roots := map[string]any{
		"body": map[string]any{"token": "t0ken-abc"},
		"raw":  `{"token":"t0ken-abc"}`,
	}
	rec := Of("checkout", apiStep(models.Secrets{
		ObservedPaths: map[string][]string{"body": {"/token"}},
	}), 1, roots, nil)

	if rec.Observed["raw"] != Redacted {
		t.Errorf("raw = %v, want withheld alongside body", rec.Observed["raw"])
	}
}

func TestObservedWithholdsAWholeRoot(t *testing.T) {
	roots := map[string]any{"raw": "tok=abc", "status": float64(200)}
	rec := Of("checkout", apiStep(models.Secrets{
		ObservedRoots: map[string]bool{"raw": true},
	}), 1, roots, nil)
	if rec.Observed["raw"] != Redacted {
		t.Errorf("raw = %v, want withheld", rec.Observed["raw"])
	}
	if rec.Observed["status"] != float64(200) {
		t.Errorf("status = %v, want it kept", rec.Observed["status"])
	}
}

// A pointer that addresses nothing is not an error: the response is a run-time
// fact, and a capture that could not read is already an errored assertion.
func TestObservedToleratesAPointerThatMissed(t *testing.T) {
	roots := map[string]any{"body": map[string]any{"other": "value"}}
	rec := Of("checkout", apiStep(models.Secrets{
		ObservedPaths: map[string][]string{"body": {"/data/access_token"}},
	}), 1, roots, nil)
	if got := rec.Observed["body"].(map[string]any)["other"]; got != "value" {
		t.Errorf("body.other = %v, want it untouched", got)
	}
}

// A step with no marks traces everything, which is what makes the feature worth
// having.
func TestNothingWithheldWithoutMarks(t *testing.T) {
	roots := map[string]any{"status": float64(200), "raw": "ok"}
	rec := Of("checkout", apiStep(models.Secrets{}), 1, roots, nil)
	headers := rec.Sent["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer hunter2" {
		t.Errorf("Authorization = %v: nothing was marked, so nothing is withheld", headers["Authorization"])
	}
	if rec.Observed["raw"] != "ok" {
		t.Errorf("raw = %v, want it kept", rec.Observed["raw"])
	}
}

// The case a trace helps most: no response at all, so what was sent is the whole
// of the evidence.
func TestRecordsAnErrorWithNoObservation(t *testing.T) {
	rec := Of("checkout", apiStep(models.Secrets{}), 3, nil, errors.New("connection refused"))
	if rec.Error != "connection refused" {
		t.Errorf("Error = %q", rec.Error)
	}
	if rec.Observed != nil {
		t.Errorf("Observed = %v, want none", rec.Observed)
	}
	if rec.Attempt != 3 {
		t.Errorf("Attempt = %d, want 3", rec.Attempt)
	}
}

func TestTruncatesALongValueAndSaysSo(t *testing.T) {
	long := strings.Repeat("x", MaxBytes+500)
	rec := Of("checkout", apiStep(models.Secrets{}), 1, map[string]any{"raw": long}, nil)

	got := rec.Observed["raw"].(string)
	if len(got) > MaxBytes+80 {
		t.Errorf("the value was not cut: %d bytes", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a cut value must say so in the value")
	}
	if len(rec.Truncated) != 1 || rec.Truncated[0] != "observed.raw" {
		t.Errorf("Truncated = %v, want [observed.raw]", rec.Truncated)
	}
}

// A truncated value stays valid UTF-8, so the file stays valid JSON.
func TestTruncationKeepsValidUTF8(t *testing.T) {
	// A three-byte rune straddling the limit.
	long := strings.Repeat("a", MaxBytes-1) + strings.Repeat("→", 10)
	rec := Of("checkout", apiStep(models.Secrets{}), 1, map[string]any{"raw": long}, nil)
	doc, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("the trace will not encode: %v", err)
	}
	var back Record
	if err := json.Unmarshal(doc, &back); err != nil {
		t.Fatalf("the trace will not decode: %v", err)
	}
}

func TestWriterIsOffWithoutADirectory(t *testing.T) {
	for _, w := range []*Writer{nil, New("")} {
		path, err := w.On(Record{Scenario: "s", Step: "one"})
		if err != nil || path != "" {
			t.Errorf("On() = %q, %v, want \"\", nil", path, err)
		}
	}
}

func TestWriterWritesOneFilePerStep(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "traces")
	w := New(dir)

	first, err := w.On(Record{Scenario: "checkout", Step: "get a token"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "checkout-get-a-token.json"); first != want {
		t.Errorf("path = %q, want %q", first, want)
	}
	if _, err := os.Stat(first); err != nil {
		t.Errorf("the file was not written: %v", err)
	}

	// Two steps sharing a name must not overwrite each other.
	second, err := w.On(Record{Scenario: "checkout", Step: "get a token"})
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Errorf("the second step reused %q", first)
	}
}

// Nothing is created until something is written, so a run with tracing off --
// or one that ran no step -- leaves no empty folder.
func TestWriterCreatesNothingUntilItWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "traces")
	New(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the directory exists before any write: %v", err)
	}
}

// The second withholding layer. A service that echoes a credential back puts it
// somewhere no capture named, so the marks cannot reach it and only the value
// itself can be matched.
func TestScrubCatchesAnEchoedCredential(t *testing.T) {
	roots := map[string]any{
		// No mark names this: the step has no secret capture. The service simply
		// handed the token back.
		"body": map[string]any{"echoed": "t0ken-abc"},
		"raw":  `{"echoed":"t0ken-abc"}`,
	}
	rec := Of("checkout", apiStep(models.Secrets{}), 1, roots, nil, "t0ken-abc")

	doc, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "t0ken-abc") {
		t.Errorf("the echoed credential survived:\n%s", doc)
	}
	if got := rec.Observed["body"].(map[string]any)["echoed"]; got != Redacted {
		t.Errorf("echoed = %v, want withheld", got)
	}
}

// Scrubbing reaches a value inside a longer string, which is the whole reason it
// is not just an equality check.
func TestScrubReachesInsideAString(t *testing.T) {
	roots := map[string]any{"raw": "Set-Cookie: session=t0ken-abc; Path=/"}
	rec := Of("checkout", apiStep(models.Secrets{}), 1, roots, nil, "t0ken-abc")
	got := rec.Observed["raw"].(string)
	if strings.Contains(got, "t0ken-abc") {
		t.Errorf("raw = %q, want the value withheld", got)
	}
	if !strings.Contains(got, "Path=/") {
		t.Errorf("raw = %q, want the rest of the header kept", got)
	}
}

// A value too short to be distinctive is left alone: replacing every "1" would
// destroy the trace rather than protect anything.
func TestScrubIgnoresAValueTooShortToChase(t *testing.T) {
	roots := map[string]any{"raw": `{"count":1,"status":"ok"}`}
	rec := Of("checkout", apiStep(models.Secrets{}), 1, roots, nil, "1")
	if got := rec.Observed["raw"].(string); got != `{"count":1,"status":"ok"}` {
		t.Errorf("raw = %q, want it untouched", got)
	}
}

// A longer value that contains a shorter one must be replaced first, or the
// shorter replacement cuts the longer value in half and leaves part of it.
func TestScrubReplacesTheLongestValueFirst(t *testing.T) {
	roots := map[string]any{"raw": "secret-abcdef"}
	rec := Of("checkout", apiStep(models.Secrets{}), 1, roots, nil, "secret", "secret-abcdef")
	if got := rec.Observed["raw"].(string); got != Redacted {
		t.Errorf("raw = %q, want the whole value withheld", got)
	}
}

// The error text is scrubbed too: a connection error can quote the URL, and a
// URL can carry a credential in a query parameter.
func TestScrubReachesTheErrorText(t *testing.T) {
	rec := Of("checkout", apiStep(models.Secrets{}), 1, nil,
		errorsNew("Get \"https://api.test?t=t0ken-abc\": dial tcp: refused"), "t0ken-abc")
	if strings.Contains(rec.Error, "t0ken-abc") {
		t.Errorf("Error = %q, want the credential withheld", rec.Error)
	}
}

func errorsNew(s string) error { return errors.New(s) }
