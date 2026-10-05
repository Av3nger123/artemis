package eval

import (
	"errors"
	"testing"

	"artemis/pkg/dsl/parser"
)

// apiEnv is an api step's observation: the shapes a decoded HTTP response
// arrives in, plus a scenario var and a capture.
func apiEnv() *Env {
	return &Env{
		Roots: map[string]any{
			"status": float64(200),
			"body": map[string]any{
				"data": map[string]any{
					"count": float64(0),
					"email": "ada@example.com",
					"roles": []any{"admin", "staff"},
					"token": "t-1",
					"meta":  map[string]any{"page": float64(2)},
					"nil":   nil,
				},
				"ok": true,
			},
			"raw":     `{"data":{"count":0}}`,
			"headers": Headers{"content-type": "application/json", "x-request-id": "r-9"},
		},
		Vars: map[string]any{
			"url":   "https://api.example.com",
			"token": "t-1",
			"limit": float64(10),
		},
		Getenv: func(name string) string {
			if name == "API_URL" {
				return "https://env.example.com"
			}
			return ""
		},
	}
}

// evalSrc parses src as an expression and evaluates it. A parse error is a
// broken test, not a result: this package is given trees the front end
// accepted.
func evalSrc(t *testing.T, src string, env *Env) (any, error) {
	t.Helper()
	x, bag := parser.ParseExpr("t.art", src)
	if bag.HasErrors() {
		t.Fatalf("ParseExpr(%q) reported %d diagnostics: %v", src, bag.Len(), bag.All())
	}
	return Eval(x, env)
}

func TestLookupPrefersARootOverAVar(t *testing.T) {
	env := apiEnv()
	env.Vars["status"] = float64(500)

	got, err := evalSrc(t, "status", env)
	if err != nil {
		t.Fatalf("status errored: %v", err)
	}
	if got != float64(200) {
		t.Errorf("status = %v, want 200 -- a capture must not shadow the step's own root", got)
	}
}

func TestVarsAndCapturesResolve(t *testing.T) {
	env := apiEnv()
	for src, want := range map[string]any{
		"url":   "https://api.example.com",
		"limit": float64(10),
	} {
		got, err := evalSrc(t, src, env)
		if err != nil {
			t.Fatalf("%s errored: %v", src, err)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", src, got, want)
		}
	}
}

func TestEnvFunctionReadsTheProcessEnvironment(t *testing.T) {
	got, err := evalSrc(t, `env("API_URL")`, apiEnv())
	if err != nil {
		t.Fatalf(`env("API_URL") errored: %v`, err)
	}
	if got != "https://env.example.com" {
		t.Errorf(`env("API_URL") = %v, want the configured value`, got)
	}
}

// TestEnvAcceptsTwoArguments is the evaluator's half of ART-25's arity
// change. What the default value *produces* is Task 2's subject -- getenv
// keeps its one-parameter signature until then, so a two-argument call still
// evaluates to the variable's value. What this asserts is only that two
// arguments are no longer an arity error.
func TestEnvAcceptsTwoArguments(t *testing.T) {
	env := &Env{Getenv: func(string) string { return "" }}
	if _, err := evalSrc(t, `env("PORT", "8080")`, env); err != nil {
		t.Fatalf("env() with two arguments: %v", err)
	}
}

// TestEnvRejectsThreeArgumentsAtRunTime is evalCall's arity check standing in
// for the checker: this package must be total over a tree the checker never
// saw, so a mistake the checker would have caught still has to fail here.
func TestEnvRejectsThreeArgumentsAtRunTime(t *testing.T) {
	env := &Env{Getenv: func(string) string { return "" }}
	if _, err := evalSrc(t, `env("A", "b", "c")`, env); err == nil {
		t.Fatal("three arguments were accepted")
	}
}

// An unset name is the empty string, which is today's documented behaviour: an
// absent variable is how a scenario says "no token".
func TestEnvFunctionIsEmptyForAnUnsetName(t *testing.T) {
	got, err := evalSrc(t, `env("NOPE")`, apiEnv())
	if err != nil {
		t.Fatalf(`env("NOPE") errored: %v`, err)
	}
	if got != "" {
		t.Errorf(`env("NOPE") = %q, want ""`, got)
	}
}

func TestEnvFunctionDefaultsToTheRealEnvironment(t *testing.T) {
	t.Setenv("ARTEMIS_EVAL_TEST", "set")
	got, err := evalSrc(t, `env("ARTEMIS_EVAL_TEST")`, &Env{})
	if err != nil {
		t.Fatalf("errored: %v", err)
	}
	if got != "set" {
		t.Errorf("env() = %q, want the process value when Getenv is nil", got)
	}
}

func TestANilEnvResolvesNothingAndDoesNotPanic(t *testing.T) {
	if _, err := evalSrc(t, "status", nil); err == nil {
		t.Fatal("status against a nil Env returned no error")
	}
	if _, err := evalSrc(t, "1 == 1", nil); err != nil {
		t.Fatalf("a closed expression against a nil Env errored: %v", err)
	}
}

// page is an Elements fake. ART-45 brings the real one; this proves the
// dispatch, the arguments and the error paths now.
type page struct {
	texts   map[string]string
	visible map[string]bool
	counts  map[string]float64
	attrs   map[[2]string]string
	err     error
}

func (p page) Text(sel string) (any, error) {
	if p.err != nil {
		return nil, p.err
	}
	v, ok := p.texts[sel]
	if !ok {
		return nil, errors.New("no element matches " + sel)
	}
	return v, nil
}
func (p page) Value(sel string) (any, error)   { return p.Text(sel) }
func (p page) Count(sel string) (any, error)   { return p.counts[sel], nil }
func (p page) Visible(sel string) (any, error) { return p.visible[sel], nil }
func (p page) Attr(sel, n string) (any, error) { return p.attrs[[2]string{sel, n}], nil }

func browserEnv() *Env {
	return &Env{
		Roots: map[string]any{
			"page": map[string]any{"url": "https://app.example.com/billing/confirmed", "title": "Billing"},
		},
		Elements: page{
			texts:   map[string]string{"[role=status]": "Pro", ".invoice-total": "$42.00"},
			visible: map[string]bool{".invoice-preview": true, ".modal": false},
			counts:  map[string]float64{".invoice": 3},
			attrs:   map[[2]string]string{{"#link", "href"}: "/billing"},
		},
	}
}

func TestElementFunctionsDispatch(t *testing.T) {
	env := browserEnv()
	cases := []struct {
		src  string
		want any
	}{
		{`text("[role=status]")`, "Pro"},
		{`value(".invoice-total")`, "$42.00"},
		{`visible(".invoice-preview")`, true},
		{`visible(".modal")`, false},
		{`count(".invoice")`, float64(3)},
		{`attr("#link", "href")`, "/billing"},
		{`page.url`, "https://app.example.com/billing/confirmed"},
		{`page.title`, "Billing"},
	}
	for _, c := range cases {
		got, err := evalSrc(t, c.src, env)
		if err != nil {
			t.Errorf("%s errored: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// A selector that matches nothing is the page's own reason, carried through as
// the assertion's.
func TestAnElementErrorIsCarriedThrough(t *testing.T) {
	_, err := evalSrc(t, `text(".missing")`, browserEnv())
	if err == nil || err.Error() != "no element matches .missing" {
		t.Fatalf("text(\".missing\") error = %v, want the page's own reason", err)
	}
}

// The checker rejects an element function outside a browser step, so this is
// only reachable through a tree built in Go or decoded from JSON. It is a
// reason, never a nil-pointer panic.
func TestAnElementFunctionWithNoPageIsAReason(t *testing.T) {
	_, err := evalSrc(t, `visible(".modal")`, apiEnv())
	if err == nil {
		t.Fatal("visible() in an api step returned no error")
	}
	if want := "visible() needs a browser page, and this step has none"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
