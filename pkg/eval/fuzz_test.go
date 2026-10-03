package eval

import (
	"errors"
	"testing"

	"artemis/pkg/dsl/parser"
)

// seeds are expressions worth starting from: one of each operator, the shapes
// that error, and a few the parser only half-builds.
var seeds = []string{
	"status == 200",
	"body.data.count > 0",
	`body.data.email matches /.+@.+/`,
	`body.data.roles contains "admin"`,
	"body.data.token exists",
	"body.data.count is number",
	"not body.data.token exists",
	"status == 200 and body.ok",
	"status == 200 or status == 201",
	`headers["content-type"] contains "json"`,
	"arr[0]",
	"-num",
	`env("API_URL")`,
	`visible(".modal")`,
	`"${url}/orders/${body.data.id}"`,
	`{"a": [1, {"b": null}]}`,
	"status ==",
	"((((",
	"",
}

// FuzzEval asserts the two things that have to hold for arbitrary bytes rather
// than for the expressions someone thought to write down.
//
//  1. Nothing panics. An expression is evaluated against a *live* observation,
//     so the evaluator is the one stage whose inputs are not under the
//     author's control: a response of the wrong shape must be an errored
//     assertion, never a stack trace. That is the value ART-6 and ART-7
//     established and pkg/dsl's front end continued.
//  2. Every failure is an *Error with a reason. An error of some other type
//     would reach a report with no span and, worse, a message nobody wrote for
//     a person to read.
func FuzzEval(f *testing.F) {
	for _, src := range seeds {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		x, _ := parser.ParseExpr("fuzz.art", src)
		// A tree the parser reported on is evaluated anyway: the point is that
		// a half-built tree is a reason and not a crash.
		env := typesEnv()
		env.Elements = page{texts: map[string]string{".x": "x"}}

		if _, err := Eval(x, env); err != nil {
			requireEvalError(t, src, err)
		}

		o := Assert(x, env)
		if o.Err != nil {
			requireEvalError(t, src, o.Err)
		}

		// Recording is part of the path a run takes, and a reason with no text
		// would print an empty line where the failure should be.
		rec := o.Record("step", "expect", 1)
		if o.Err != nil && rec.Error == "" {
			t.Fatalf("%q: an errored assertion recorded no reason", src)
		}
		if rec.Describe() == "" {
			t.Fatalf("%q: an assertion described itself as nothing", src)
		}
	})
}

func requireEvalError(t *testing.T, src string, err error) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%q: error is %T (%v), want an *eval.Error", src, err, err)
	}
	if e.Reason == "" {
		t.Fatalf("%q: an *eval.Error carried no reason", src)
	}
}
