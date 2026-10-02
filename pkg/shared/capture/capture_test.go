package capture

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// src builds a Source the way an executor does: the raw text, plus that text
// decoded as a JSON object when it is one.
func src(text string) Source {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		parsed = nil
	}
	return Source{Text: []byte(text), JSON: parsed}
}

func step(captures map[string]models.Capture) models.Step {
	return models.Step{Name: "login", Type: "api", Capture: captures}
}

func TestAJSONCaptureKeepsItsType(t *testing.T) {
	body := `{"token": "abc", "count": 7, "ok": true, "user": {"id": 1}, "tags": ["a"], "none": null}`
	s := step(map[string]models.Capture{
		"token": {JSON: "$.token"},
		"count": {JSON: "$.count"},
		"ok":    {JSON: "$.ok"},
		"user":  {JSON: "$.user"},
		"tags":  {JSON: "$.tags"},
		"none":  {JSON: "$.none"},
	})

	vars := map[string]any{}
	if got := Apply(s, src(body), vars); got != nil {
		t.Fatalf("Apply() = %v, want no assertions", got)
	}

	want := map[string]any{
		"token": "abc",
		"count": float64(7),
		"ok":    true,
		"user":  map[string]any{"id": float64(1)},
		"tags":  []any{"a"},
		"none":  nil,
	}
	if !reflect.DeepEqual(vars, want) {
		t.Errorf("vars = %#v, want %#v", vars, want)
	}
}

// A capture is plumbing, not a check: when it works it leaves no trace in the
// assertions, so a run that captured a token still prints only its checks.
func TestASuccessfulCaptureRecordsNoAssertion(t *testing.T) {
	s := step(map[string]models.Capture{"token": {JSON: "$.token"}})
	if got := Apply(s, src(`{"token": "abc"}`), map[string]any{}); len(got) != 0 {
		t.Errorf("Apply() = %v, want no assertions", got)
	}
}

func TestAStepWithNoCapturesDoesNothing(t *testing.T) {
	vars := map[string]any{}
	if got := Apply(models.Step{Name: "ping"}, src(`{"a": 1}`), vars); got != nil {
		t.Errorf("Apply() = %v, want nil", got)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %#v, want it untouched", vars)
	}
}

func TestARegexCaptureReadsTheRawText(t *testing.T) {
	cases := []struct {
		name string
		text string
		re   string
		want string
	}{
		{"group one", "Location: /items/42\n", `/items/([0-9]+)`, "42"},
		{"no group is the whole match", "build 1.2.3 ok", `[0-9]+\.[0-9]+\.[0-9]+`, "1.2.3"},
		{"not json at all", "<html><b>ada</b></html>", `<b>(.*)</b>`, "ada"},
		{"first match wins", "id=1 id=2", `id=([0-9])`, "1"},
		{"across lines", "one\nid: 9\n", `(?s)id: (.)`, "9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars := map[string]any{}
			got := Apply(step(map[string]models.Capture{"v": {Regex: c.re}}), src(c.text), vars)
			if len(got) != 0 {
				t.Fatalf("Apply() = %v, want no assertions", got)
			}
			if vars["v"] != c.want {
				t.Errorf("vars[v] = %#v, want %q", vars["v"], c.want)
			}
		})
	}
}

// Whatever the matched text looks like, it stays text: a capture of "007" is
// "007", not 7.
func TestARegexCaptureIsAlwaysAString(t *testing.T) {
	vars := map[string]any{}
	Apply(step(map[string]models.Capture{"v": {Regex: `id=(\d+)`}}), src("id=007"), vars)
	if got, ok := vars["v"].(string); !ok || got != "007" {
		t.Errorf("vars[v] = %#v, want the string \"007\"", vars["v"])
	}
}

func TestACaptureThatCannotBeMadeIsAnErroredAssertion(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		capture models.Capture
		wantIn  string
		wantOp  string
	}{
		{"path does not resolve", `{"a": 1}`, models.Capture{JSON: "$.nope.deeper"}, "$.nope.deeper", "json"},
		{"output is not json", `<html>no</html>`, models.Capture{JSON: "$.a"}, "no parsed JSON output", "json"},
		{"regex matches nothing", `{"a": 1}`, models.Capture{Regex: "zzz"}, "matched nothing", "regex"},
		{"regex will not compile", `{"a": 1}`, models.Capture{Regex: "("}, "will not compile", "regex"},
		{"no source at all", `{"a": 1}`, models.Capture{}, "no json or regex", "json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars := map[string]any{}
			got := Apply(step(map[string]models.Capture{"v": c.capture}), src(c.text), vars)
			if len(got) != 1 {
				t.Fatalf("Apply() = %v, want one assertion", got)
			}
			a := got[0]
			if a.Status != result.StatusError || a.Kind != Kind {
				t.Errorf("assertion = %+v, want an errored capture", a)
			}
			if a.Path != "v" {
				t.Errorf("assertion Path = %q, want the capture key", a.Path)
			}
			if a.Operator != c.wantOp {
				t.Errorf("assertion Operator = %q, want %q", a.Operator, c.wantOp)
			}
			if a.Step != "login" {
				t.Errorf("assertion Step = %q, want the step name", a.Step)
			}
			if !strings.Contains(a.Error, c.wantIn) {
				t.Errorf("assertion error = %q, want it to mention %q", a.Error, c.wantIn)
			}
			if _, ok := vars["v"]; ok {
				t.Error("a failed capture still wrote to the scope")
			}
		})
	}
}

// A regex never needs the output to be JSON, so it reads a body a JSON path
// cannot: the two sources fail and succeed independently in the same step.
func TestARegexSucceedsWhereAJSONPathCannot(t *testing.T) {
	vars := map[string]any{}
	got := Apply(step(map[string]models.Capture{
		"fromJSON":  {JSON: "$.name"},
		"fromRegex": {Regex: `<b>(.*)</b>`},
	}), src("<html><b>ada</b></html>"), vars)

	if len(got) != 1 {
		t.Fatalf("Apply() = %v, want one errored assertion", got)
	}
	if got[0].Path != "fromJSON" {
		t.Errorf("errored assertion = %+v, want it to be the json capture", got[0])
	}
	if vars["fromRegex"] != "ada" {
		t.Errorf("vars[fromRegex] = %#v, want \"ada\"", vars["fromRegex"])
	}
	if _, ok := vars["fromJSON"]; ok {
		t.Error("the failed json capture still wrote to the scope")
	}
}

// Two mistyped paths should take one run to find, not two -- and they come out
// in sorted order so the report reads the same every time.
func TestEveryBadCaptureIsReportedInSortedOrder(t *testing.T) {
	vars := map[string]any{}
	got := Apply(step(map[string]models.Capture{
		"zebra": {JSON: "$.nope"},
		"apple": {JSON: "$.also.nope"},
		"ok":    {JSON: "$.a"},
	}), src(`{"a": 1}`), vars)

	if len(got) != 2 {
		t.Fatalf("Apply() = %v, want two errored assertions", got)
	}
	if got[0].Path != "apple" || got[1].Path != "zebra" {
		t.Errorf("assertion keys = %q, %q; want apple, zebra", got[0].Path, got[1].Path)
	}
	if vars["ok"] != float64(1) {
		t.Errorf("vars[ok] = %#v, want the good capture to have been made anyway", vars["ok"])
	}
}

// A captured value overwrites a variable of the same name; everything else in
// the scope is left alone.
func TestACaptureOverwritesOnlyItsOwnKey(t *testing.T) {
	vars := map[string]any{"token": "stale", "base": "http://x"}
	Apply(step(map[string]models.Capture{"token": {JSON: "$.token"}}), src(`{"token": "fresh"}`), vars)
	if vars["token"] != "fresh" {
		t.Errorf("vars[token] = %#v, want \"fresh\"", vars["token"])
	}
	if vars["base"] != "http://x" {
		t.Errorf("vars[base] = %#v, want it left alone", vars["base"])
	}
}

// A path the jsonpath library panics on must fail its step, not the run.
func TestAPathThatPanicsIsAnError(t *testing.T) {
	for _, path := range []string{"$.", "$[", "$.a[", "$[?(@.a"} {
		vars := map[string]any{}
		got := Apply(step(map[string]models.Capture{"v": {JSON: path}}), src(`{"a": 1}`), vars)
		if len(got) != 1 || got[0].Status != result.StatusError {
			t.Errorf("Apply(%q) = %v, want one errored assertion", path, got)
		}
	}
}

// ErrNoJSON is matched by a test rather than by its wording, so the message can
// be reworded without breaking anything.
func TestNoJSONIsASentinel(t *testing.T) {
	if !errors.Is(readJSONErr(t), ErrNoJSON) {
		t.Error("a capture from output that was not JSON does not wrap ErrNoJSON")
	}
}

func readJSONErr(t *testing.T) error {
	t.Helper()
	_, err := readJSON("$.a", nil)
	return err
}

// The JSON paths a scenario actually writes. Carried over from the test that
// covered shared.ExtractValue, which this package replaced.
func TestTheJSONPathsAScenarioWrites(t *testing.T) {
	body := `{
		"token": "abc",
		"id": 7,
		"ok": true,
		"user": {"name": "ada", "roles": ["admin", "dev"]},
		"items": [{"sku": "x1"}, {"sku": "x2"}]
	}`

	cases := []struct {
		name string
		path string
		want any
	}{
		{"top-level string", "$.token", "abc"},
		{"number stays a float64", "$.id", float64(7)},
		{"bool", "$.ok", true},
		{"nested", "$.user.name", "ada"},
		{"array index", "$.items[0].sku", "x1"},
		{"second element", "$.items[1].sku", "x2"},
		{"inside a nested array", "$.user.roles[1]", "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars := map[string]any{}
			if got := Apply(step(map[string]models.Capture{"v": {JSON: c.path}}), src(body), vars); len(got) != 0 {
				t.Fatalf("Apply(%q) = %v, want no assertions", c.path, got)
			}
			if vars["v"] != c.want {
				t.Errorf("capture of %q = %#v, want %#v", c.path, vars["v"], c.want)
			}
		})
	}
}

func TestTheJSONPathsThatDoNotResolve(t *testing.T) {
	body := `{"token": "abc", "items": []}`

	for _, path := range []string{"$.nope", "$.token.deeper", "$.items[0]", "not a path"} {
		t.Run(path, func(t *testing.T) {
			vars := map[string]any{}
			got := Apply(step(map[string]models.Capture{"v": {JSON: path}}), src(body), vars)
			if len(got) != 1 || got[0].Status != result.StatusError {
				t.Errorf("Apply(%q) = %v, want one errored assertion", path, got)
			}
			if _, ok := vars["v"]; ok {
				t.Errorf("capture of %q still wrote to the scope", path)
			}
		})
	}
}
