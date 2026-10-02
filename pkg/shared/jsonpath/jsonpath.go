// Package jsonpath is the only place in artemis that names a JSON-path library.
//
// Two call sites need paths -- pkg/shared/assert, which must tell a malformed
// path from one that does not resolve, and pkg/shared/capture, which reads one
// value and reports whatever went wrong -- and both came through here so that
// swapping the library underneath is a one-file change. The library is
// github.com/ohler55/ojg/jp (ART-19); it replaced github.com/oliveagle/jsonpath,
// which had no release since 2018.
//
// The surface is deliberately small: compile a path, look a value up, and one
// shorthand for doing both at once. Nothing here returns an ojg type, so no
// caller can start depending on one.
package jsonpath

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
)

// ErrNotFound is what a path that resolves to nothing reports. Callers match it
// rather than its wording, and it is what lets `exists` be answered without
// reading an error message.
//
// It is returned only for a path that can match at most one value. A path with a
// wildcard, a descent or a filter answers "nothing matched" with an empty slice
// instead -- see Lookup.
var ErrNotFound = errors.New("nothing is at that path")

// Path is a compiled JSON path.
type Path struct {
	raw string
	// expr is the compiled path. single is jp's Normal(): true when the path is
	// nothing but root, child and index steps, so it can match at most one
	// value.
	expr   jp.Expr
	single bool
}

// Compile parses path and reports why it is not a path if it is not one.
//
// A path must start with "$". ojg will happily parse `nonsense` as a path
// relative to the current node, which would turn a plainly mistyped path into a
// value that merely fails to resolve; every path artemis documents is rooted, so
// requiring the root keeps the error pointed at the real mistake.
func Compile(path string) (Path, error) {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, "$") {
		return Path{}, fmt.Errorf("a path must start with $")
	}
	expr, err := jp.ParseString(trimmed)
	if err != nil {
		return Path{}, err
	}
	return Path{raw: trimmed, expr: expr, single: expr.Normal()}, nil
}

// String returns the path as it was written.
func (p Path) String() string { return p.raw }

// Lookup reads the value p names in data.
//
// A path that can match at most one value -- `$.data.id`, `$.items[0].sku` --
// returns that value, with the type encoding/json gave it, and ErrNotFound when
// there is nothing at it. A JSON null that is present is a value: it returns nil
// with no error, which is how a check can tell a null field from a missing one.
//
// A path that can match many -- `$.items[*].name`, `$..name`, `$.*`, a filter --
// returns every match as a []any, in the order the library found them, and an
// empty slice rather than an error when nothing matched: "no element matched" is
// an answer for `contains` to fail on, not a broken path.
func (p Path) Lookup(data any) (any, error) {
	if p.single {
		val, ok := p.expr.FirstFound(data)
		if !ok {
			return nil, ErrNotFound
		}
		return val, nil
	}
	got := p.expr.Get(data)
	if got == nil {
		// Get returns a nil slice when nothing matched; hand back an empty one
		// so a caller can range over the result without a nil check.
		return []any{}, nil
	}
	return got, nil
}

// Lookup compiles path and reads it in one step, for a caller that has no reason
// to keep the compiled path around.
func Lookup(path string, data any) (any, error) {
	p, err := Compile(path)
	if err != nil {
		return nil, err
	}
	return p.Lookup(data)
}
