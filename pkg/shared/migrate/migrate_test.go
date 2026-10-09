package migrate

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/check"
	"artemis/pkg/dsl/parser"
)

// source is Source with the error folded into the test, because almost every
// case here is about what came out and not whether anything did.
func source(t *testing.T, config Config) string {
	t.Helper()
	src, err := Source(config, Comments{})
	if err != nil {
		t.Fatalf("Source = error %v", err)
	}
	return src
}

// checks reports whether src also passes the name checker, which is what the
// corpus test needs per file and what proves a migrated scope is complete: a
// `capture` this package wrote has to be resolvable by the steps after it.
func checks(t *testing.T, src string) []string {
	t.Helper()
	tree, bag := parser.Parse("t.art", src)
	_, checked := check.Check(tree)
	bag.Merge(checked)

	var out []string
	for _, d := range bag.All() {
		out = append(out, string(d.Code)+": "+d.Message)
	}
	return out
}

// apiStepFor is the smallest api step worth migrating.
func apiStepFor(name string) Step {
	return Step{
		Name:     name,
		Type:     apiStep,
		Request:  Request{URL: "{{base}}/items", Method: "GET"},
		Response: Response{StatusCode: 200},
	}
}

// A whole api scenario, end to end: the variable, the verb, the header, the
// body as an object literal, the assertions and the capture, in the layout the
// canonical printer gives them.
func TestSourceAPIScenario(t *testing.T) {
	config := Config{
		Name:      "item lifecycle",
		Type:      "functional",
		Variables: []Variable{{Name: "base", Value: "{{env.API_URL}}"}},
		Steps: []Step{{
			Name: "create item",
			Type: apiStep,
			Request: Request{
				URL:     "{{base}}/items",
				Method:  "POST",
				Headers: map[string]string{"Content-Type": "application/json", "Authorization": "Bearer {{token}}"},
				Body:    `{"name": "widget"}`,
			},
			Response: Response{StatusCode: 201, Body: []BodyCheck{
				{Path: "$.id", Operator: "exists"},
			}},
			Capture: map[string]Capture{"itemId": {JSON: "$.id"}},
			Timeout: "5s",
			Retry:   Retry{Times: 3, Delay: "50ms"},
		}},
	}

	want := `scenario "item lifecycle" {
  var base = env("API_URL")

  step "create item" {
    post "${base}/items" {
      header "Authorization" = "Bearer ${token}"
      header "Content-Type" = "application/json"
      body = {"name": "widget"}
    }
    timeout = "5s"
    retry { times = 3, delay = "50ms" }
    expect status == 201
    expect body.id exists
    capture itemId = body.id
  }
}
`
	if got := source(t, config); got != want {
		t.Errorf("Source =\n%s\nwant\n%s", got, want)
	}
}

// An exec step, with both streams and a capture out of stdout. `run` is what
// makes it a terminal step; there is no type key in the output at all.
func TestSourceExecScenario(t *testing.T) {
	config := Config{
		Name: "api and terminal",
		Steps: []Step{{
			Name: "work out the path",
			Type: execStep,
			Exec: Exec{
				Command: "sh",
				Args:    []string{"-c", "printf items"},
				Cwd:     "/tmp",
				Stdin:   "in",
				Env:     map[string]string{"B": "2", "A": "1"},
			},
			Expect: Expect{
				Stdout: []TextCheck{{Operator: "equals", Value: "items"}},
				Stderr: []TextCheck{{Value: "nothing"}},
			},
			Capture: map[string]Capture{
				"path":  {Regex: "items"},
				"build": {JSON: "$.build"},
			},
		}},
	}

	want := `scenario "api and terminal" {
  step "work out the path" {
    run "sh" {
      args = ["-c", "printf items"]
      cwd = "/tmp"
      stdin = "in"
      env { A = "1", B = "2" }
    }
    expect exit_code == 0
    expect stdout == "items"
    expect stderr contains "nothing"
    capture build = stdout.build
    capture path = match(stdout, /items/)
  }
}
`
	if got := source(t, config); got != want {
		t.Errorf("Source =\n%s\nwant\n%s", got, want)
	}
}

// A step with no `response:` block still asserts its status, because
// httpstep does: `statusOK := resp.status == step.Response.StatusCode` runs
// whatever the scenario wrote. The migrated file reproduces the assertion the
// run made, failure and all, rather than quietly dropping it.
func TestSourceKeepsTheStatusAssertionAStepNeverWrote(t *testing.T) {
	config := Config{Name: "n", Steps: []Step{{
		Name: "ping", Type: apiStep, Request: Request{URL: "u", Method: "GET"},
	}}}
	if got := source(t, config); !strings.Contains(got, "expect status == 0") {
		t.Errorf("Source =\n%s\nwant it to hold `expect status == 0`", got)
	}
}

// An empty `method:` is a GET, because http.NewRequestWithContext sends one.
func TestSourceAnEmptyMethodIsAGet(t *testing.T) {
	config := Config{Name: "n", Steps: []Step{{
		Name: "ping", Type: apiStep, Request: Request{URL: "u"},
		Response: Response{StatusCode: 200},
	}}}
	if got := source(t, config); !strings.Contains(got, `get "u"`) {
		t.Errorf("Source =\n%s\nwant it to hold `get \"u\"`", got)
	}
}

// A body that is not JSON stays a string, which is what a form-encoded or
// plain-text payload needs.
func TestSourceBodies(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"name": "widget"}`, `body = {"name": "widget"}`},
		{`[1, 2]`, "body = [1, 2]"},
		{`{"token": "{{tokn}}"}`, `body = {"token": "${tokn}"}`},
		{"user=ada&id=1", `body = "user=ada&id=1"`},
		{`{"n": {{count}}}`, `body = "{\"n\": ${count}}"`},
		{"42", `body = "42"`},
		{`{"id": 12345678901234567890}`, `body = {"id": 12345678901234567890}`},
	}
	for _, c := range cases {
		config := Config{Name: "n", Steps: []Step{{
			Name: "post it", Type: apiStep,
			Request:  Request{URL: "u", Method: "POST", Body: c.body},
			Response: Response{StatusCode: 200},
		}}}
		got := source(t, config)
		if !strings.Contains(got, c.want) {
			t.Errorf("body %q migrated to\n%s\nwant it to hold %s", c.body, got, c.want)
		}
	}
}

// retry writes only the fields the scenario gave it: `delay = ""` is not a
// duration the DSL accepts, so a bare `retry: 3` must not emit one.
func TestSourceRetry(t *testing.T) {
	cases := []struct {
		retry Retry
		want  string
	}{
		{Retry{}, ""},
		{Retry{Times: 3}, "retry { times = 3 }"},
		{Retry{Delay: "1s"}, `retry { delay = "1s" }`},
		{Retry{Times: 3, Delay: "1s"}, `retry { times = 3, delay = "1s" }`},
	}
	for _, c := range cases {
		step := apiStepFor("ping")
		step.Request.URL = "u"
		step.Retry = c.retry
		got := source(t, Config{Name: "n", Steps: []Step{step}})
		if c.want == "" {
			if strings.Contains(got, "retry") {
				t.Errorf("retry %#v migrated to\n%s\nwant no retry block", c.retry, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("retry %#v migrated to\n%s\nwant it to hold %s", c.retry, got, c.want)
		}
	}
}

// Everything Source writes resolves: a capture is in scope for the steps after
// it and for nothing before, which is the checker's own rule and the one a
// hand-rolled emitter is most likely to get wrong.
func TestSourceOutputPassesTheChecker(t *testing.T) {
	first := apiStepFor("sign in")
	first.Capture = map[string]Capture{"token": {JSON: "$.token"}}
	second := apiStepFor("use it")
	second.Request.URL = "{{base}}/items/{{token}}"

	config := Config{
		Name:      "scope",
		Variables: []Variable{{Name: "base", Value: "%SERVER%"}},
		Steps:     []Step{first, second},
	}
	if diags := checks(t, source(t, config)); len(diags) != 0 {
		t.Errorf("migrated source has diagnostics: %v", diags)
	}
}

func TestSourceRefusals(t *testing.T) {
	cases := []struct {
		name     string
		step     Step
		mentions string
	}{
		{"a step type with no action block",
			Step{Name: "n", Type: "browser"}, `step type "browser"`},
		{"a method the DSL has no verb for",
			Step{Name: "n", Type: apiStep, Request: Request{URL: "u", Method: "TRACE"}}, "TRACE"},
		{"an unclosed placeholder in a url",
			Step{Name: "n", Type: apiStep, Request: Request{URL: "{{base", Method: "GET"}}, "unclosed"},
		{"`empty` on a stream",
			Step{Name: "n", Type: execStep, Exec: Exec{Command: "sh"},
				Expect: Expect{Stderr: []TextCheck{{Operator: "empty"}}}}, "empty"},
		{"a capture path that matches many",
			Step{Name: "n", Type: apiStep, Request: Request{URL: "u", Method: "GET"},
				Capture: map[string]Capture{"x": {JSON: "$..id"}}}, "recursive descent"},
		{"a bare $ capture on a terminal step",
			Step{Name: "n", Type: execStep, Exec: Exec{Command: "sh"},
				Capture: map[string]Capture{"x": {JSON: "$"}}}, "unparsed text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Source(Config{Name: "n", Steps: []Step{c.step}}, Comments{})
			if err == nil {
				t.Fatal("Source = nil error, want one")
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("error = %q, want it to mention %q", err, c.mentions)
			}
			// The step is named, because a suite's author needs to know which
			// one to go and rewrite.
			if !strings.Contains(err.Error(), `step 1 "n"`) {
				t.Errorf("error = %q, want it to name the step", err)
			}
		})
	}
}

// A bare `$` on an api step is `body`, the parsed document, which is what the
// YAML capture read. Only the terminal step has no equivalent.
func TestSourceBareDollarOnAnAPIStep(t *testing.T) {
	step := apiStepFor("ping")
	step.Request.URL = "u"
	step.Capture = map[string]Capture{"all": {JSON: "$"}}
	if got := source(t, Config{Name: "n", Steps: []Step{step}}); !strings.Contains(got, "capture all = body") {
		t.Errorf("Source =\n%s\nwant it to hold `capture all = body`", got)
	}
}

// The two comment positions that carry, and the count of the ones that do not.
func TestReadComments(t *testing.T) {
	raw := []byte(`# the file
# second line
name: "n"
type: functional # trailing, dropped
variables:
  # above a variable, dropped
  - name: base
    value: "x"
steps:
  # the first step
  - name: "a"
    type: api
  - name: "b"
    type: api
`)
	c := ReadComments(raw)
	if want := "# the file\n# second line"; c.File != want {
		t.Errorf("File = %q, want %q", c.File, want)
	}
	if len(c.Steps) != 2 {
		t.Fatalf("Steps = %q, want two", c.Steps)
	}
	if want := "# the first step"; c.Steps[0] != want {
		t.Errorf("Steps[0] = %q, want %q", c.Steps[0], want)
	}
	if c.Steps[1] != "" {
		t.Errorf("Steps[1] = %q, want nothing", c.Steps[1])
	}
	if c.Dropped != 2 {
		t.Errorf("Dropped = %d, want 2", c.Dropped)
	}
}

// A file with no comments drops none, and a file that is not a mapping at all
// costs the comments rather than erroring -- Source is still what refuses it.
func TestReadCommentsOnFilesWithNothingToRead(t *testing.T) {
	for _, raw := range []string{"name: n\n", "", "- 1\n", "}{\n"} {
		c := ReadComments([]byte(raw))
		if c.File != "" || len(c.Steps) != 0 || c.Dropped != 0 {
			t.Errorf("ReadComments(%q) = %#v, want the zero value", raw, c)
		}
	}
}

// Both blocks land where canonical layout puts a comment: above the scenario
// and above the step.
func TestSourceCarriesComments(t *testing.T) {
	config := Config{Name: "n", Steps: []Step{apiStepFor("ping")}}
	config.Steps[0].Request.URL = "u"
	comments := Comments{File: "# what this file is for", Steps: []string{"# what this step does"}}

	src, err := Source(config, comments)
	if err != nil {
		t.Fatal(err)
	}
	want := `# what this file is for
scenario "n" {
  # what this step does
  step "ping" {
    get "u"
    expect status == 200
  }
}
`
	if src != want {
		t.Errorf("Source =\n%s\nwant\n%s", src, want)
	}
}

// Text that was a comment in the YAML file cannot become source in the .art
// one, whatever shape the comment field arrived in.
func TestSourceCommentsAreAlwaysComments(t *testing.T) {
	src, err := Source(Config{Name: "n"}, Comments{File: "no hash here"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(src, "# no hash here\n") {
		t.Errorf("Source =\n%s\nwant it to open with a commented line", src)
	}
}

// A YAML scenario may capture a name twice -- a login and a refresh that both
// keep token -- and a .art file may not bind it twice. The second capture is
// written token_2, every later read follows it, and the file says so.
func TestSourceRenamesARecapturedName(t *testing.T) {
	login := apiStepFor("login")
	login.Request = Request{URL: "{{base}}/token", Method: "POST"}
	login.Capture = map[string]Capture{"token": {JSON: "$.token"}}
	refresh := apiStepFor("refresh")
	refresh.Request = Request{URL: "{{base}}/refresh", Method: "POST", Headers: map[string]string{"Authorization": "Bearer {{token}}"}}
	refresh.Capture = map[string]Capture{"token": {JSON: "$.token"}, "base": {JSON: "$.base"}}
	again := apiStepFor("again")
	again.Request = Request{URL: "{{base}}/refresh", Method: "POST", Headers: map[string]string{"Authorization": "Bearer {{token}}"}}
	again.Capture = map[string]Capture{"token": {JSON: "$.token"}, "token_2": {JSON: "$.other"}}
	me := apiStepFor("me")
	me.Request = Request{URL: "{{base}}/me", Headers: map[string]string{"Authorization": "Bearer {{token}} {{token_2}}"}}

	src, err := Source(Config{
		Name:      "poll",
		Variables: []Variable{{Name: "base", Value: "http://h"}},
		Steps:     []Step{login, refresh, again, me},
	}, Comments{File: "# poll a token"})
	if err != nil {
		t.Fatal(err)
	}
	want := `# poll a token
# The capture "base" of step 2 "refresh" is written base_2 here, because a name is bound once and base already is.
# The capture "token" of step 2 "refresh" is written token_3 here, because a name is bound once and token already is.
# The capture "token" of step 3 "again" is written token_4 here, because a name is bound once and token already is.
scenario "poll" {
  var base = "http://h"

  step "login" {
    post "${base}/token"
    expect status == 200
    capture token = body.token
  }

  step "refresh" {
    post "${base}/refresh" { header "Authorization" = "Bearer ${token}" }
    expect status == 200
    capture base_2 = body.base
    capture token_3 = body.token
  }

  step "again" {
    post "${base_2}/refresh" { header "Authorization" = "Bearer ${token_3}" }
    expect status == 200
    capture token_4 = body.token
    capture token_2 = body.other
  }

  step "me" {
    get "${base_2}/me" { header "Authorization" = "Bearer ${token_4} ${token_2}" }
    expect status == 200
  }
}
`
	if src != want {
		t.Errorf("Source =\n%s\nwant\n%s", src, want)
	}
	if diags := checks(t, src); len(diags) != 0 {
		t.Errorf("the migrated file does not check: %v", diags)
	}
}
