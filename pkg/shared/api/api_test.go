package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"artemis/pkg/result"
	"artemis/pkg/shared/models"
)

// vars is the config map CallAPI renders a request against.
func vars() *map[string]interface{} {
	m := map[string]interface{}{
		"base":  "",
		"token": "sekret",
		"id":    float64(7),
	}
	return &m
}

func step(name string, r models.Request, wantStatus int) models.Step {
	return models.Step{Name: name, Type: "api", Request: r, Response: models.Response{StatusCode: wantStatus}}
}

func TestCallAPISendsTheRenderedRequest(t *testing.T) {
	var got struct {
		method string
		path   string
		header string
		body   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.header, got.body = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	config := vars()
	(*config)["base"] = srv.URL

	resp, err := CallAPI(step("create", models.Request{
		URL:     "{{base}}/items/{{id}}",
		Method:  http.MethodPost,
		Headers: map[string]string{"Authorization": "Bearer {{token}}"},
		Body:    `{"id": {{id}}}`,
	}, 200), config)
	if err != nil {
		t.Fatalf("CallAPI() = %v, want nil", err)
	}
	defer resp.Body.Close()

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/items/7" {
		t.Errorf("path = %q, want /items/7 -- the captured id should render without a decimal point", got.path)
	}
	if got.header != "Bearer sekret" {
		t.Errorf("Authorization = %q, want %q", got.header, "Bearer sekret")
	}
	if got.body != `{"id": 7}` {
		t.Errorf("body = %q, want %q", got.body, `{"id": 7}`)
	}
}

// Every placeholder in a request is rendered before anything is sent, and a
// placeholder that cannot be rendered says which part of the request it was in.
func TestCallAPIRenderErrorsNameWhatFailedToRender(t *testing.T) {
	cases := []struct {
		name    string
		request models.Request
		wantIn  string
	}{
		{"url", models.Request{URL: "{{nope}}/x", Method: http.MethodGet}, "rendering request url"},
		{"body", models.Request{URL: "http://127.0.0.1:1", Method: http.MethodGet, Body: "{{nope}}"}, "rendering request body"},
		{
			"header",
			models.Request{URL: "http://127.0.0.1:1", Method: http.MethodGet, Headers: map[string]string{"X-Token": "{{nope}}"}},
			`rendering header "X-Token"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := CallAPI(step("s", c.request, 200), vars())
			if err == nil {
				resp.Body.Close()
				t.Fatal("CallAPI() = nil error, want a render error before any request is sent")
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantIn)
			}
			if !strings.Contains(err.Error(), "unknown variable") {
				t.Errorf("error = %q, want it to carry the templater's reason", err)
			}
			if resp != nil {
				t.Error("CallAPI() returned a response alongside an error")
			}
		})
	}
}

func TestCallAPIRejectsAnUnusableRequest(t *testing.T) {
	cases := []struct {
		name    string
		request models.Request
		wantIn  string
	}{
		{"bad method", models.Request{URL: "http://127.0.0.1:1", Method: "GET POST"}, "error creating request"},
		{"no scheme", models.Request{URL: "127.0.0.1/x", Method: http.MethodGet}, "error performing request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := CallAPI(step("s", c.request, 200), vars())
			if err == nil {
				resp.Body.Close()
				t.Fatal("CallAPI() = nil error, want a failure")
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantIn)
			}
		})
	}
}

// ParseResponse hands the runner a map to assert and capture against. Anything
// it cannot read is nil rather than an error: the status assertion has already
// recorded that the response was not what the scenario asked for, and a body
// that is not JSON is reported by the assertions that then do not resolve.
func TestParseResponse(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantStatus int
		body       string
		want       map[string]interface{}
	}{
		{"json object", 200, 200, `{"id": 1, "nested": {"a": true}}`, map[string]interface{}{"id": float64(1), "nested": map[string]interface{}{"a": true}}},
		{"empty object", 200, 200, `{}`, map[string]interface{}{}},
		{"not json", 200, 200, `<html>no</html>`, nil},
		{"empty body", 200, 200, ``, nil},
		{"json array is not an object", 200, 200, `[1, 2]`, nil},
		{"unexpected status is not parsed", 500, 200, `{"error": "boom"}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader(c.body))}

			got := ParseResponse(step("s", models.Request{}, c.wantStatus), resp)
			if len(got) != len(c.want) {
				t.Fatalf("ParseResponse() = %#v, want %#v", got, c.want)
			}
			for k, want := range c.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("ParseResponse() has no %q", k)
				} else if _, isMap := want.(map[string]interface{}); !isMap && gotV != want {
					t.Errorf("ParseResponse()[%q] = %#v, want %#v", k, gotV, want)
				}
			}
		})
	}
}

func TestParseResponseOfNilIsNil(t *testing.T) {
	if got := ParseResponse(step("s", models.Request{}, 200), nil); got != nil {
		t.Errorf("ParseResponse(nil) = %#v, want nil", got)
	}
}

func TestAllPassed(t *testing.T) {
	pass := result.AssertionResult{Status: result.StatusPass}
	fail := result.AssertionResult{Status: result.StatusFail}
	errored := result.AssertionResult{Status: result.StatusError}
	skip := result.AssertionResult{Status: result.StatusSkip}

	cases := []struct {
		name string
		in   []result.AssertionResult
		want bool
	}{
		{"none", nil, true},
		{"empty", []result.AssertionResult{}, true},
		{"all passed", []result.AssertionResult{pass, pass}, true},
		{"a skip does not fail it", []result.AssertionResult{pass, skip}, true},
		{"one failed", []result.AssertionResult{pass, fail}, false},
		{"one errored", []result.AssertionResult{pass, errored}, false},
		{"first failed", []result.AssertionResult{fail, pass}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AllPassed(c.in); got != c.want {
				t.Errorf("AllPassed(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestAssertResponseReturnsOneResultPerCheck(t *testing.T) {
	s := step("s", models.Request{}, 200)
	s.Response.Body = []models.BodyCheck{
		{Path: "$.status", Value: "ok"},
		{Path: "$.count", Value: 2},
	}

	got := AssertResponse(s, map[string]interface{}{"status": "ok", "count": float64(3)})
	if len(got) != 2 {
		t.Fatalf("AssertResponse() returned %d results, want 2", len(got))
	}
	if !got[0].Passed() {
		t.Errorf("first assertion = %s, want pass", got[0].Status)
	}
	if got[1].Passed() {
		t.Errorf("second assertion = %s, want fail", got[1].Status)
	}
}

func TestExecuteScriptsCapturesIntoTheConfig(t *testing.T) {
	data := map[string]interface{}{
		"token": "abc",
		"user":  map[string]interface{}{"id": float64(7)},
		"items": []interface{}{map[string]interface{}{"sku": "x1"}},
	}
	s := step("login", models.Request{}, 200)
	s.Scripts = []models.Script{
		{Key: "authToken", Path: "$.token"},
		{Key: "userId", Path: "$.user.id"},
		{Key: "sku", Path: "$.items[0].sku"},
	}

	config := vars()
	if err := ExecuteScripts(data, s, config); err != nil {
		t.Fatalf("ExecuteScripts() = %v, want nil", err)
	}
	for key, want := range map[string]interface{}{"authToken": "abc", "userId": float64(7), "sku": "x1"} {
		if got := (*config)[key]; got != want {
			t.Errorf("config[%q] = %#v, want %#v", key, got, want)
		}
	}
	// Variables that were already there are untouched.
	if got := (*config)["token"]; got != "sekret" {
		t.Errorf("config[%q] = %#v, want it left alone", "token", got)
	}
}

func TestExecuteScriptsMissingPathIsAnErrorNamingThePath(t *testing.T) {
	s := step("login", models.Request{}, 200)
	s.Scripts = []models.Script{{Key: "authToken", Path: "$.nope.deeper"}}

	config := vars()
	err := ExecuteScripts(map[string]interface{}{"token": "abc"}, s, config)
	if err == nil {
		t.Fatal("ExecuteScripts() = nil, want an error for a path that does not resolve")
	}
	if !strings.Contains(err.Error(), "$.nope.deeper") {
		t.Errorf("error = %q, want it to name the path", err)
	}
	if _, ok := (*config)["authToken"]; ok {
		t.Error("a failed capture still wrote to the config")
	}
}

func TestExecuteScriptsWithNoScriptsChangesNothing(t *testing.T) {
	config := vars()
	before := len(*config)

	if err := ExecuteScripts(map[string]interface{}{"a": 1}, step("s", models.Request{}, 200), config); err != nil {
		t.Fatalf("ExecuteScripts() = %v, want nil", err)
	}
	if len(*config) != before {
		t.Errorf("config grew to %d entries, want %d", len(*config), before)
	}
}

// A step with scripts but no body to read them from is the runner's problem, not
// this package's: ExecuteScripts is never handed a nil map by the runner, and if
// it is, a missing path is still an error rather than a panic.
func TestExecuteScriptsOnANilBodyIsAnErrorNotAPanic(t *testing.T) {
	s := step("s", models.Request{}, 200)
	s.Scripts = []models.Script{{Key: "k", Path: "$.a"}}

	err := ExecuteScripts(nil, s, vars())
	if err == nil {
		t.Fatal("ExecuteScripts(nil) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "$.a") {
		t.Errorf("error = %q, want it to name the path", err)
	}
}
