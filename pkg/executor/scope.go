package executor

// Scope is the variable map a scenario carries from step to step: the variables
// it declared, with whatever later steps captured written over the top.
//
// It is a map type rather than a struct because that is what it is -- the
// templating in pkg/shared takes a plain map[string]interface{}, and Vars hands
// it one without copying. It replaces the *map[string]interface{} threaded
// through the runner today, where the pointer bought nothing.
//
// A Scope must be made with NewScope. Set on a nil Scope panics, as writing to
// any nil map does; Get, Unset and Vars on one are fine.
type Scope map[string]any

// NewScope returns an empty scope ready to be written to.
func NewScope() Scope {
	return make(Scope)
}

// ScopeOf returns a scope holding vars. The map is taken as it stands, not
// copied: the caller is handing the scope over, not lending it.
func ScopeOf(vars map[string]any) Scope {
	if vars == nil {
		return NewScope()
	}
	return Scope(vars)
}

// Set records a value under key, replacing whatever was there. This is how a
// step captures a value for the steps after it.
func (s Scope) Set(key string, value any) {
	s[key] = value
}

// Unset removes key, so the scope holds nothing under it -- not nil, nothing.
// This is what a write that failed does: the name stops holding the value it
// had before, so a later read finds it missing rather than quietly reading a
// value that is out of date. Unsetting a key that is not there, or unsetting on
// a nil scope, does nothing.
func (s Scope) Unset(key string) {
	delete(s, key)
}

// Get returns the value under key and whether there was one. A key that was set
// to nil is present: "set to nothing" and "never set" are different answers.
func (s Scope) Get(key string) (any, bool) {
	v, ok := s[key]
	return v, ok
}

// Vars is the scope as the plain map the templating takes. It is the scope
// itself, not a copy, so a caller that writes to it writes to the scope.
func (s Scope) Vars() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return s
}
