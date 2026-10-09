package ast

import (
	"reflect"

	"artemis/pkg/dsl/token"
)

var tokenType = reflect.TypeOf(token.Token{})

// Clone deep-copies a subtree and passes every token through stamp.
//
// It is reflective rather than a switch per node type so that a node added to
// this package is cloned correctly without a second edit here: the rule is
// "copy every pointer, slice and interface; map every token.Token", and that
// rule does not change when a node gains a field. pkg/dsl/expand is the user,
// stamping Span.Via on what it copies out of a collection.
func Clone[T Node](n T, stamp func(token.Token) token.Token) T {
	if isNil(n) {
		return n
	}
	out := cloneValue(reflect.ValueOf(n), stamp)
	return out.Interface().(T)
}

func cloneValue(v reflect.Value, stamp func(token.Token) token.Token) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		cp := reflect.New(v.Elem().Type())
		cp.Elem().Set(cloneValue(v.Elem(), stamp))
		return cp
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := cloneValue(v.Elem(), stamp)
		out := reflect.New(v.Type()).Elem()
		out.Set(inner)
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(cloneValue(v.Index(i), stamp))
		}
		return cp
	case reflect.Struct:
		if v.Type() == tokenType {
			return reflect.ValueOf(stamp(v.Interface().(token.Token)))
		}
		cp := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			cp.Field(i).Set(cloneValue(v.Field(i), stamp))
		}
		return cp
	default:
		return v
	}
}
