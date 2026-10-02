package executor

import "sort"

// Registry maps a step type, in the spelling a scenario writes, to the executor
// that runs it.
//
// It is also meant to be the one list of step types artemis has. Today
// models.StepTypes declares the list and its own comment admits that nothing
// makes the runner agree with it; Types is what that list is to be derived from,
// so the next step type is added in one place.
type Registry struct {
	byType map[string]Executor
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byType: make(map[string]Executor)}
}

// Register makes e the executor for stepType.
//
// It panics on an empty type, a nil executor, or a type that is already
// registered. All three are wiring mistakes: registration happens in an init or
// a setup function, so a panic is as close to a compile error as this gets, and
// the alternative -- one of two executors silently winning -- is a bug nobody
// would find from a test run.
func (r *Registry) Register(stepType string, e Executor) {
	if stepType == "" {
		panic("executor: Register with an empty step type")
	}
	if e == nil {
		panic("executor: Register(" + stepType + ") with a nil executor")
	}
	if r.byType == nil {
		r.byType = make(map[string]Executor)
	}
	if _, exists := r.byType[stepType]; exists {
		panic("executor: step type " + stepType + " is registered twice")
	}
	r.byType[stepType] = e
}

// Lookup returns the executor for stepType and whether there is one. The match
// is exact: "HTTP" is not "http", so the set of spellings that work stays equal
// to the set that is documented.
func (r *Registry) Lookup(stepType string) (Executor, bool) {
	if r == nil || r.byType == nil {
		return nil, false
	}
	e, ok := r.byType[stepType]
	return e, ok
}

// Types returns every registered step type, sorted, so an error message and a
// validator read the same on every run.
func (r *Registry) Types() []string {
	if r == nil || len(r.byType) == 0 {
		return nil
	}
	types := make([]string, 0, len(r.byType))
	for t := range r.byType {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// defaultRegistry is the registry the binary uses. Each step type registers
// itself into it; the runner and the validator read it.
var defaultRegistry = NewRegistry()

// Default returns the registry the binary uses.
func Default() *Registry {
	return defaultRegistry
}

// Register makes e the executor for stepType in the default registry, under the
// same rules as Registry.Register.
func Register(stepType string, e Executor) {
	defaultRegistry.Register(stepType, e)
}
