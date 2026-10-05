package eval

import (
	"reflect"
	"strings"
	"testing"

	"artemis/pkg/dsl/ast"
	"artemis/pkg/dsl/parser"
	"artemis/pkg/dsl/token"
)

// The seven things an operand can be. Every operator's table below has a row
// for each of them, which is what "every operator against every type" means in
// this package: the six JSON types plus absent, the state a path that did not
// resolve is in.
//
//	null      nul
//	boolean   yes
//	number    num
//	string    str
//	array     arr
//	object    obj
//	absent    body.nope
func typesEnv() *Env {
	return &Env{
		Roots: map[string]any{
			"body":    map[string]any{"data": map[string]any{"count": float64(0)}},
			"headers": Headers{"content-type": "application/json"},
			"raw":     `{"data":{"count":0}}`,
			"status":  float64(200),
		},
		Vars: map[string]any{
			"nul":    nil,
			"yes":    true,
			"no":     false,
			"num":    float64(3),
			"numstr": "3",
			"str":    "admin team",
			"arr":    []any{float64(1), "a", nil},
			"obj":    map[string]any{"k": float64(1), "nested": map[string]any{"deep": "v"}},
			"pat":    ".+@.+",
		},
	}
}

// row is one expression and what evaluating it must produce: a value, or a
// reason containing errWant. A row with neither is a pass-through for a value
// of nil.
type row struct {
	src     string
	want    any
	errWant string
	absent  bool
}

func runRows(t *testing.T, name string, rows []row) {
	t.Helper()
	env := typesEnv()
	for _, r := range rows {
		t.Run(name+"/"+r.src, func(t *testing.T) {
			got, err := evalSrc(t, r.src, env)
			if r.errWant != "" {
				if err == nil {
					t.Fatalf("%s = %v, want the reason %q", r.src, got, r.errWant)
				}
				if !strings.Contains(err.Error(), r.errWant) {
					t.Fatalf("%s errored %q, want it to contain %q", r.src, err.Error(), r.errWant)
				}
				if IsAbsent(err) != r.absent {
					t.Errorf("%s: IsAbsent = %v, want %v", r.src, IsAbsent(err), r.absent)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s errored: %v", r.src, err)
			}
			if !reflect.DeepEqual(got, r.want) {
				t.Errorf("%s = %#v, want %#v", r.src, got, r.want)
			}
		})
	}
}

func TestEqualityAcrossEveryType(t *testing.T) {
	runRows(t, "eq", []row{
		{src: "nul == null", want: true},
		{src: "yes == true", want: true},
		{src: "num == 3", want: true},
		{src: "num == 4", want: false},
		{src: `numstr == 3`, want: true},
		{src: `str == "admin team"`, want: true},
		{src: `arr == [1, "a", null]`, want: true},
		{src: `obj == {"k": 1, "nested": {"deep": "v"}}`, want: true},
		{src: `headers == {"content-type": "application/json"}`, want: true},
		{src: "status == 200", want: true},
		{src: `status == "200"`, want: true},

		// Across types, with nothing to coerce: false, not a reason. Both
		// sides are there and comparable; they are simply not equal.
		{src: "nul == 0", want: false},
		{src: "nul == false", want: false},
		{src: `nul == ""`, want: false},
		{src: "yes == 1", want: false},
		{src: `num == "three"`, want: false},
		{src: "num == arr", want: false},
		{src: "arr == obj", want: false},
		{src: `obj == "k"`, want: false},

		{src: "num != 4", want: true},
		{src: "num != 3", want: false},
		{src: `str != "other"`, want: true},

		// An absent path has no value to compare, on either side.
		{src: "body.nope == 1", errWant: "body.nope did not resolve", absent: true},
		{src: "1 == body.nope", errWant: "body.nope did not resolve", absent: true},

		// A regex is a value only so matches can take one.
		{src: "num == /3/", errWant: "a regex belongs on the right of matches"},
	})
}

func TestOrderingAcrossEveryType(t *testing.T) {
	runRows(t, "order", []row{
		{src: "num > 2", want: true},
		{src: "num > 3", want: false},
		{src: "num >= 3", want: true},
		{src: "num < 4", want: true},
		{src: "num <= 2", want: false},
		{src: "body.data.count > -1", want: true},
		{src: "numstr > 2", want: true},
		{src: "-num < 0", want: true},

		// Neither side has an ordering, so neither has a false worth printing.
		{src: "nul > 0", errWant: "> needs a number at nul, got null"},
		{src: "yes > 0", errWant: "> needs a number at yes, got boolean"},
		{src: `str > 0`, errWant: "> needs a number at str, got string"},
		{src: "arr > 0", errWant: "> needs a number at arr, got array"},
		{src: "obj > 0", errWant: "> needs a number at obj, got object"},
		{src: "body > 0", errWant: "> needs a number at body, got object"},
		{src: "body.nope > 0", errWant: "body.nope did not resolve", absent: true},
		{src: "num > obj", errWant: "> needs a number at obj, got object"},
		{src: `num >= "x"`, errWant: `>= needs a number at "x", got string`},
		{src: "num < nul", errWant: "< needs a number at nul, got null"},
		{src: "num <= yes", errWant: "<= needs a number at yes, got boolean"},
	})
}

func TestContainsAcrossEveryType(t *testing.T) {
	runRows(t, "contains", []row{
		// A string: a substring.
		{src: `str contains "admin"`, want: true},
		{src: `str contains "owner"`, want: false},
		{src: `raw contains "count"`, want: true},
		{src: `str contains 3`, want: false},

		// An array: an element, compared the way == compares.
		{src: `arr contains 1`, want: true},
		{src: `arr contains "a"`, want: true},
		{src: `arr contains null`, want: true},
		{src: `arr contains 2`, want: false},
		{src: `["a"] contains "a"`, want: true},

		// An object: a key.
		{src: `obj contains "k"`, want: true},
		{src: `obj contains "missing"`, want: false},
		{src: `headers contains "Content-Type"`, want: true},

		{src: `nul contains "x"`, errWant: "contains needs a string, array or object at nul, got null"},
		{src: `yes contains "x"`, errWant: "contains needs a string, array or object at yes, got boolean"},
		{src: `num contains "x"`, errWant: "contains needs a string, array or object at num, got number"},
		{src: `body.nope contains "x"`, errWant: "body.nope did not resolve", absent: true},
		{src: `str contains arr`, errWant: "contains against a string needs a string at arr, got array"},
		{src: `str contains nul`, errWant: "contains against a string needs a string at nul, got null"},
		{src: `obj contains 1`, errWant: "contains against an object needs a key name at 1, got number"},
	})
}

func TestMatchesAcrossEveryType(t *testing.T) {
	runRows(t, "matches", []row{
		{src: `str matches /admin/`, want: true},
		{src: `str matches /^admin/`, want: true},
		{src: `str matches /owner/`, want: false},
		{src: `"ada@example.com" matches /.+@.+/`, want: true},
		{src: `"ada@example.com" matches pat`, want: true},
		{src: `num matches /3/`, want: true},
		{src: `yes matches /true/`, want: true},
		{src: `status matches /^2/`, want: true},

		{src: `nul matches /x/`, errWant: "matches needs a string at nul, got null"},
		{src: `arr matches /x/`, errWant: "matches needs a string at arr, got array"},
		{src: `obj matches /x/`, errWant: "matches needs a string at obj, got object"},
		{src: `body.nope matches /x/`, errWant: "body.nope did not resolve", absent: true},
		{src: `str matches 3`, errWant: "matches needs a regular expression at 3, got number"},
		{src: `str matches arr`, errWant: "matches needs a regular expression at arr, got array"},
		{src: `str matches "order-(\\d+"`, errWant: "bad regular expression"},
	})
}

func TestExistsAcrossEveryType(t *testing.T) {
	runRows(t, "exists", []row{
		{src: "yes exists", want: true},
		{src: "num exists", want: true},
		{src: "str exists", want: true},
		{src: "arr exists", want: true},
		{src: "obj exists", want: true},
		{src: "body.data.count exists", want: true},
		{src: `headers["content-type"] exists`, want: true},

		// A path that resolves to JSON null counts as absent, which is
		// pkg/shared/assert's rule and what a scenario written against it
		// expects.
		{src: "nul exists", want: false},

		// Absence is exists' answer, not its error. This is the only operator
		// that converts it.
		{src: "body.nope exists", want: false},
		{src: "body.data.nope exists", want: false},
		{src: "arr[9] exists", want: false},
		{src: "not body.nope exists", want: true},
		{src: "not body.data.count exists", want: false},

		// A failure that is not absence is still reported.
		{src: "num.field exists", errWant: "cannot read field field of a number at num"},
	})
}

func TestIsTypeAcrossEveryType(t *testing.T) {
	runRows(t, "is", []row{
		{src: "nul is null", want: true},
		{src: "yes is boolean", want: true},
		{src: "num is number", want: true},
		{src: "str is string", want: true},
		{src: "arr is array", want: true},
		{src: "obj is object", want: true},
		{src: "headers is object", want: true},
		{src: "body.data.count is number", want: true},
		{src: "status is string", want: false},
		{src: "num is string", want: false},
		{src: "nul is object", want: false},
		{src: "arr is object", want: false},
		{src: "obj is array", want: false},
		{src: `numstr is number`, want: false},

		{src: "body.nope is number", errWant: "body.nope did not resolve", absent: true},
		{src: "num is nonsense", errWant: "unknown type name nonsense"},
	})
}

func TestLogicalOperatorsTakeBooleansOnly(t *testing.T) {
	runRows(t, "logical", []row{
		{src: "yes and yes", want: true},
		{src: "yes and no", want: false},
		{src: "no or yes", want: true},
		{src: "no or no", want: false},
		{src: "not yes", want: false},
		{src: "not no", want: true},
		{src: "status == 200 and body.data.count == 0", want: true},
		{src: "(status == 200 or status == 201) and num > 2", want: true},
		{src: "not status == 201", want: true},

		// No truthiness: an operand that is not a boolean is a reason.
		{src: "num and yes", errWant: "and needs a boolean at num, got number"},
		{src: "yes and str", errWant: "and needs a boolean at str, got string"},
		{src: "nul or yes", errWant: "or needs a boolean at nul, got null"},
		{src: "arr and yes", errWant: "and needs a boolean at arr, got array"},
		{src: "obj or no", errWant: "or needs a boolean at obj, got object"},
		{src: "not num", errWant: "not needs a boolean at num, got number"},
		{src: "not body.nope", errWant: "body.nope did not resolve", absent: true},
	})
}

// Short-circuiting is what keeps `no and body.nope == 1` a verdict rather than
// a reason, and it is what the Python and JavaScript targets will do, so ART-48
// lowers mechanically.
func TestLogicalOperatorsShortCircuit(t *testing.T) {
	runRows(t, "shortcircuit", []row{
		{src: "no and body.nope == 1", want: false},
		{src: "no and num", want: false},
		{src: "yes or body.nope == 1", want: true},
		{src: "yes or num", want: true},
		// The side that decides nothing is still evaluated.
		{src: "yes and body.nope == 1", errWant: "body.nope did not resolve", absent: true},
		{src: "no or body.nope == 1", errWant: "body.nope did not resolve", absent: true},
	})
}

func TestUnaryMinusAcrossEveryType(t *testing.T) {
	runRows(t, "minus", []row{
		{src: "-num", want: float64(-3)},
		{src: "-3", want: float64(-3)},
		{src: "-numstr", want: float64(-3)},
		{src: "-body.data.count", want: float64(0)},
		{src: "-nul", errWant: "- needs a number at nul, got null"},
		{src: "-yes", errWant: "- needs a number at yes, got boolean"},
		{src: "-str", errWant: "- needs a number at str, got string"},
		{src: "-arr", errWant: "- needs a number at arr, got array"},
		{src: "-obj", errWant: "- needs a number at obj, got object"},
		{src: "-body.nope", errWant: "body.nope did not resolve", absent: true},
	})
}

func TestFieldAccessAcrossEveryType(t *testing.T) {
	runRows(t, "member", []row{
		{src: "obj.k", want: float64(1)},
		{src: "obj.nested.deep", want: "v"},
		{src: "body.data.count", want: float64(0)},
		{src: "headers.nope", errWant: "headers.nope did not resolve", absent: true},

		// A field of null is absent, so `exists` can answer about a body whose
		// data key is null.
		{src: "nul.anything", errWant: "nul.anything did not resolve", absent: true},
		{src: "obj.missing", errWant: "obj.missing did not resolve", absent: true},
		{src: "obj.nested.missing", errWant: "obj.nested.missing did not resolve", absent: true},

		// A field of anything else is a mistake, not a missing value.
		{src: "yes.field", errWant: "cannot read field field of a boolean at yes"},
		{src: "num.field", errWant: "cannot read field field of a number at num"},
		{src: "str.field", errWant: "cannot read field field of a string at str"},
		{src: "arr.field", errWant: "cannot read field field of an array at arr"},
	})
}

func TestIndexingAcrossEveryType(t *testing.T) {
	runRows(t, "index", []row{
		{src: "arr[0]", want: float64(1)},
		{src: "arr[1]", want: "a"},
		{src: "arr[2]", want: nil},
		{src: `obj["k"]`, want: float64(1)},
		{src: `headers["Content-Type"]`, want: "application/json"},
		{src: `headers["content-type"]`, want: "application/json"},
		{src: `body["data"]["count"]`, want: float64(0)},
		{src: "arr[body.data.count]", want: float64(1)},

		// Out of range, negative, or a key that is not there: absent, so
		// `exists` answers about it.
		{src: "arr[3]", errWant: "arr[3] did not resolve", absent: true},
		{src: "arr[-1]", errWant: "arr[-1] did not resolve", absent: true},
		{src: `obj["missing"]`, errWant: `obj["missing"] did not resolve`, absent: true},
		{src: `nul["k"]`, errWant: `nul["k"] did not resolve`, absent: true},

		// An index of the wrong shape is the mistake, so nothing is coerced.
		{src: `arr["0"]`, errWant: `an array index must be a number, got string at "0"`},
		{src: "arr[nul]", errWant: "an array index must be a number, got null at nul"},
		{src: "arr[0.5]", errWant: "an array index must be a whole number, got 0.5 at 0.5"},
		{src: "obj[0]", errWant: "an object key must be a string, got number at 0"},
		{src: `str["k"]`, errWant: "cannot index a string at str"},
		{src: `num[0]`, errWant: "cannot index a number at num"},
		{src: `yes[0]`, errWant: "cannot index a boolean at yes"},
	})
}

func TestLiteralsAndComposites(t *testing.T) {
	runRows(t, "literal", []row{
		{src: "42", want: float64(42)},
		{src: "3.5", want: 3.5},
		{src: "1e3", want: float64(1000)},
		{src: `"text"`, want: "text"},
		{src: "true", want: true},
		{src: "false", want: false},
		{src: "null", want: nil},
		{src: "(num)", want: float64(3)},
		{src: `["-f", "seed.sql"]`, want: []any{"-f", "seed.sql"}},
		{src: `[]`, want: []any{}},
		{src: `{"username": "alice", "password": str}`, want: map[string]any{"username": "alice", "password": "admin team"}},
		{src: `{}`, want: map[string]any{}},
		{src: `{"nested": {"n": [1]}}`, want: map[string]any{"nested": map[string]any{"n": []any{float64(1)}}}},
		{src: `{"${str}": 1}`, want: map[string]any{"admin team": float64(1)}},
	})
}

// Interpolation is the other half of what this package subsumes: a URL and an
// assertion must agree about what a captured number looks like.
func TestInterpolationRendersWithART6sRules(t *testing.T) {
	runRows(t, "interp", []row{
		{src: `"${str}"`, want: "admin team"},
		{src: `"n=${num}"`, want: "n=3"},
		{src: `"${status}"`, want: "200"},
		{src: `"${body.data.count}"`, want: "0"},
		{src: `"${yes}"`, want: "true"},
		{src: `"${nul}"`, want: "null"},
		{src: `"${obj.nested}"`, want: `{"deep":"v"}`},
		{src: `"${arr}"`, want: `[1,"a",null]`},
		{src: `"${num}${num}"`, want: "33"},
		{src: `"a\${b}"`, want: "a${b}"},
		{src: `"$5.00"`, want: "$5.00"},
		{src: `"${body.nope}"`, errWant: "body.nope did not resolve", absent: true},
	})
}

// Nothing in this package reports a diagnostic, and nothing panics on a tree
// the parser recovered through: a nil expression and an *ast.Bad are reasons.
func TestABrokenTreeIsAReasonAndNotAPanic(t *testing.T) {
	if _, err := Eval(nil, typesEnv()); err == nil {
		t.Error("Eval(nil) returned no error")
	}
	if o := Assert(nil, typesEnv()); o.Err == nil {
		t.Error("Assert(nil).Err is nil, want a reason")
	}

	x, bag := parser.ParseExpr("t.art", "status ==")
	if !bag.HasErrors() {
		t.Fatal(`ParseExpr("status ==") reported nothing, want a syntax error`)
	}
	o := Assert(x, typesEnv())
	if o.Err == nil {
		t.Errorf("Assert of a half-parsed expression = %+v, want a reason", o)
	}
}

// textEnv is a step whose body is not JSON at all: `raw` is the plain text
// that arrived, `body` is null because it did not parse, and the only way to
// read a value out of it is a regex. That is pkg/cli/testdata/regex_capture.yaml's
// first step, which is what match() exists for.
func textEnv() *Env {
	return &Env{
		Roots: map[string]any{
			"status":  float64(200),
			"body":    nil,
			"raw":     "moved to /items/42",
			"headers": Headers{"content-type": "text/plain"},
		},
		Vars: map[string]any{
			"pat":    `/items/([0-9]+)`,
			"two":    `(items|orders)/([0-9]+)`,
			"padded": "id=007",
		},
	}
}

// TestMatchExtraction is the whole of match()'s contract, which SPEC.md
// states and ART-48 and ART-50 port: which group, how many, what no match
// does, and what type comes out.
func TestMatchExtraction(t *testing.T) {
	env := textEnv()
	rows := []row{
		// One capturing group gives group 1; none gives the whole match.
		{src: `match(raw, /\/items\/([0-9]+)/)`, want: "42"},
		{src: `match(raw, /\/items\/[0-9]+/)`, want: "/items/42"},
		{src: `match(raw, /(?:items|orders)\/([0-9]+)/)`, want: "42"},

		// A pattern may be a string, as it may be after `matches`, so it can
		// live in a var.
		{src: `match(raw, pat)`, want: "42"},

		// The value is a string, always. Reading "007" as seven loses the
		// zeros a zero-padded id depends on.
		{src: `match(padded, /id=([0-9]+)/)`, want: "007"},
		{src: `match(padded, /id=([0-9]+)/) is string`, want: true},
		{src: `match(padded, /id=([0-9]+)/) is number`, want: false},
		{src: `match(raw, /\/items\/([0-9]+)/) == "42"`, want: true},

		// No match is a reason, not an empty string -- and not absent, so
		// `exists` reports it rather than answering false.
		{src: `match(raw, /\/orders\/([0-9]+)/)`,
			errWant: `regex "/orders/([0-9]+)" matched nothing in raw`},
		{src: `match(raw, /\/orders\/([0-9]+)/) exists`,
			errWant: `matched nothing in raw`},

		// Two capturing groups is a mistake, not a choice. The checker
		// catches a literal pattern; this catches one out of a var.
		{src: `match(raw, two)`,
			errWant: `match() reads one capturing group, and regex "(items|orders)/([0-9]+)" has 2`},

		// The arguments, each the wrong way round.
		{src: `match(headers, /x/)`, errWant: "match()'s first argument must be a string, got object"},
		{src: `match(raw, 1)`, errWant: "match()'s second argument must be a regular expression, got number"},
		{src: `match(raw, "(a")`, errWant: `bad regular expression "(a"`},
		{src: `match(raw)`, errWant: "match() takes 2 argument(s); this call has 1"},

		// An argument that could not be evaluated is the reason, not a
		// second one about match().
		{src: `match(body.nope, /x/)`, errWant: "body.nope did not resolve", absent: true},
	}
	for _, r := range rows {
		t.Run(r.src, func(t *testing.T) {
			got, err := evalSrc(t, r.src, env)
			if r.errWant != "" {
				if err == nil {
					t.Fatalf("%s = %#v, want the reason %q", r.src, got, r.errWant)
				}
				if !strings.Contains(err.Error(), r.errWant) {
					t.Fatalf("%s errored %q, want it to contain %q", r.src, err.Error(), r.errWant)
				}
				if IsAbsent(err) != r.absent {
					t.Errorf("%s: IsAbsent = %v, want %v -- a pattern that matched "+
						"nothing is not an absent path", r.src, IsAbsent(err), r.absent)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s errored: %v", r.src, err)
			}
			if !reflect.DeepEqual(got, r.want) {
				t.Errorf("%s = %#v, want %#v", r.src, got, r.want)
			}
		})
	}
}

// TestEveryComparisonIsImplemented walks token.Comparisons rather than listing
// the operators, because compare's switch is the third copy of that set --
// the parser and the checker now share token.IsComparison, but an evaluator
// cannot be derived from a table and has to be reconciled with one instead.
//
// An operator added to Comparisons and not taught to compare would parse,
// check, pass the checker's simple/complex classifier, reach a UI's operator
// dropdown through `artemis grammar --json`, and then fail at run time with
// "unknown operator". This is what stops that.
//
// Both operands are strings, which is the one pair every comparison in the
// language accepts: ordering compares them as numbers only when they are
// numbers, and `contains` and `matches` want text. What is asserted is that
// the operator was *implemented*, not what it answered.
func TestEveryComparisonIsImplemented(t *testing.T) {
	for _, op := range token.Comparisons {
		// The three operand pairs the language's comparisons are defined
		// over, tried in turn: ordering wants numbers, `contains` wants text
		// or a collection, `matches` wants a pattern. An operator is
		// implemented if any pair gives it a true-or-false answer; the test
		// is about reachability, not about which answer.
		answered := false
		for _, src := range []string{
			"1 " + op + " 1",
			`"a" ` + op + ` "a"`,
			`"a" ` + op + ` /a/`,
		} {
			x, bag := parser.ParseExpr("t.art", src)
			if bag.HasErrors() {
				t.Errorf("%s: the parser rejected %q, which token.Comparisons says is an operator", op, src)
				break
			}
			v, err := Eval(x, typesEnv())
			if err != nil {
				if strings.Contains(err.Error(), "unknown operator") {
					t.Errorf("%s is in token.Comparisons but pkg/eval has no case for it: %v", op, err)
					break
				}
				continue // a type mismatch: try the next pair.
			}
			if _, ok := v.(bool); ok {
				answered = true
				break
			}
			t.Errorf("%s: %q evaluated to %v (%T), want a boolean", op, src, v, v)
		}
		if !answered {
			t.Errorf("%s answered none of the operand pairs; is it implemented?", op)
		}
	}
}

// TestUnknownOperatorIsReachableOnlyByABug pins the arm the test above exists
// to keep unreachable, so that deleting it would be noticed.
func TestUnknownOperatorIsReachableOnlyByABug(t *testing.T) {
	b := &ast.Binary{
		X:  &ast.Literal{Tok: token.Token{Kind: token.Number, Text: "1", Value: "1"}},
		Op: token.Token{Kind: token.Ident, Text: "beside", Value: "beside"},
		Y:  &ast.Literal{Tok: token.Token{Kind: token.Number, Text: "1", Value: "1"}},
	}
	_, err := compare(b, "beside", float64(1), float64(1))
	if err == nil || !strings.Contains(err.Error(), "unknown operator") {
		t.Errorf("compare with an invented operator = %v, want an unknown-operator error", err)
	}
}

func TestEnvWithADynamicNameErrorsAtUse(t *testing.T) {
	env := &Env{
		Vars:   map[string]any{"which": "API_URL"},
		Lookup: func(string) (string, bool) { return "", false },
	}
	_, err := evalSrc(t, `env(which)`, env)
	if err == nil {
		t.Fatal("a dynamic name with no value gave no error")
	}
	if !strings.Contains(err.Error(), Absent("API_URL").Error()) {
		t.Fatalf("the reason must be the one text:\n got: %v\nwant: %v", err, Absent("API_URL"))
	}
}
