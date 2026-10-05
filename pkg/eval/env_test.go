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
		Lookup: func(name string) (string, bool) {
			if name == "API_URL" {
				return "https://env.example.com", true
			}
			return "", false
		},
	}
}

// strPtr is env()'s second argument, as the pointer getenv now takes: a
// fallback has to be a pointer so that env("FLAG", "") can be told apart from
// env("FLAG"), which a plain string could not do.
func strPtr(s string) *string { return &s }

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

// TestEnvAcceptsTwoArguments is the evaluator's half of ART-25's arity change,
// end to end through Eval: two arguments are not an arity error, and the
// second one is the value an absent name answers with.
//
// getenv's own tests cover the default value directly. What this one adds is
// the path between them -- evalCall has to pass the second argument down, and
// a call that evaluated to the variable's value while ignoring its default
// would pass every one of those tests.
func TestEnvAcceptsTwoArguments(t *testing.T) {
	env := &Env{Lookup: func(string) (string, bool) { return "", false }}
	got, err := evalSrc(t, `env("PORT", "8080")`, env)
	if err != nil {
		t.Fatalf("env() with two arguments: %v", err)
	}
	if got != "8080" {
		t.Fatalf(`env("PORT", "8080") = %v, want the default value`, got)
	}
}

// TestEnvRejectsThreeArgumentsAtRunTime is evalCall's arity check standing in
// for the checker: this package must be total over a tree the checker never
// saw, so a mistake the checker would have caught still has to fail here.
func TestEnvRejectsThreeArgumentsAtRunTime(t *testing.T) {
	env := &Env{Lookup: func(string) (string, bool) { return "", false }}
	if _, err := evalSrc(t, `env("A", "b", "c")`, env); err == nil {
		t.Fatal("three arguments were accepted")
	}
}

// An absent name with no fallback is an error, not the empty string: ART-25
// found that the old "" let `get "${url}/orders"` silently request "/orders"
// instead of failing on the absent variable that produced it.
func TestEnvFunctionIsAnErrorForAnUnsetName(t *testing.T) {
	_, err := evalSrc(t, `env("NOPE")`, apiEnv())
	if err == nil {
		t.Fatal(`env("NOPE") gave no error`)
	}
}

func TestEnvFunctionDefaultsToTheRealEnvironment(t *testing.T) {
	t.Setenv("ARTEMIS_EVAL_TEST", "set")
	got, err := evalSrc(t, `env("ARTEMIS_EVAL_TEST")`, &Env{})
	if err != nil {
		t.Fatalf("errored: %v", err)
	}
	if got != "set" {
		t.Errorf("env() = %q, want the process value when Lookup is nil", got)
	}
}

func TestEnvAbsentIsAnError(t *testing.T) {
	for name, value := range map[string]string{
		"absent": "\x00", // the sentinel this test uses for "not set"
		"empty":  "",
		"spaces": "   ",
		"tab":    "\t",
	} {
		t.Run(name, func(t *testing.T) {
			env := &Env{Lookup: func(string) (string, bool) {
				if value == "\x00" {
					return "", false
				}
				return value, true
			}}
			if _, err := env.getenv("API_URL", nil); err == nil {
				t.Fatalf("a %s variable gave no error", name)
			}
		})
	}
}

func TestEnvKeepsTheValueItAccepts(t *testing.T) {
	env := &Env{Lookup: func(string) (string, bool) { return "  http://x  ", true }}
	got, err := env.getenv("API_URL", nil)
	if err != nil {
		t.Fatalf("a value with space at each end was rejected: %v", err)
	}
	if got != "  http://x  " {
		t.Fatalf("got %q; the value must not change", got)
	}
}

func TestEnvFallbackWhenAbsent(t *testing.T) {
	env := &Env{Lookup: func(string) (string, bool) { return "", false }}
	got, err := env.getenv("PORT", strPtr("8080"))
	if err != nil {
		t.Fatalf("a default value did not apply: %v", err)
	}
	if got != "8080" {
		t.Fatalf("got %q, want 8080", got)
	}
}

func TestEnvEmptyFallbackIsLegal(t *testing.T) {
	env := &Env{Lookup: func(string) (string, bool) { return "", false }}
	got, err := env.getenv("FLAG", strPtr(""))
	if err != nil {
		t.Fatalf(`env("FLAG", "") must give an empty value: %v`, err)
	}
	if got != "" {
		t.Fatalf("got %q, want an empty value", got)
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

// TestEnvNonStringDefaultNamesTheType is the interpreter's half of a sentence
// the two generated helpers also have to write.
//
// The checker judges only a literal, so env("PORT", port) compiles and this is
// what the step then says. pkg/codegen's art_env tests assert the part of it a
// helper can reproduce -- everything before " at ", which is the source text
// of the argument and is not something a helper has.
func TestEnvNonStringDefaultNamesTheType(t *testing.T) {
	env := &Env{
		Lookup: func(string) (string, bool) { return "", false },
		Vars:   map[string]any{"p": 8080.0},
	}
	_, err := evalSrc(t, `env("PORT", p)`, env)
	if err == nil {
		t.Fatal("a number as the default value was accepted")
	}
	want := "env()'s argument must be a string, got number at p"
	if err.Error() != want {
		t.Fatalf("message\n got: %s\nwant: %s", err, want)
	}
}
