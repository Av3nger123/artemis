package eval

import (
	"errors"
	"fmt"

	"artemis/pkg/dsl/token"
)

// Error is a question that could not be asked: an absent path, `>` against an
// object, a selector in a step with no page. It is the reason an assertion is
// *errored* rather than failed, which is the distinction pkg/shared/assert
// established and ART-12's report prints -- "that check could not be made" and
// "that check gave the wrong answer" are different things to read.
//
// It is a plain error with a span, not a diag.Diagnostic. diag is the
// compile-time vocabulary -- codes, carets, mechanical suggestions -- and this
// is a run-time fact about one response. The span is carried anyway because
// pointing a `--json` run report at the expression later then costs nothing.
type Error struct {
	Span   token.Span
	Reason string

	// absent marks the one evaluation failure that is not final: a path that
	// did not resolve. `exists` turns it into false; every other operator
	// reports it.
	absent bool
}

func (e *Error) Error() string { return e.Reason }

// errorf is an evaluation that cannot be performed.
func errorf(span token.Span, format string, args ...any) *Error {
	return &Error{Span: span, Reason: fmt.Sprintf(format, args...)}
}

// absentf is a path that did not resolve.
func absentf(span token.Span, format string, args ...any) *Error {
	e := errorf(span, format, args...)
	e.absent = true
	return e
}

// IsAbsent reports whether err is a path that did not resolve, as opposed to an
// evaluation that could not be performed for any other reason. Only `exists`
// treats the two differently, but a step type that grows its own "not there
// yet" handling will want the same test.
func IsAbsent(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.absent
}
