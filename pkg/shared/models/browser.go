package models

// This file is the `browser` step type's action: the statements the step runs
// in the page. What it expects of the page is an `expect` expression held by
// pkg/dsl/lower and evaluated against what pkg/steps/browserstep observed, not
// a field here -- the same division the other two step types already follow
// (ART-40).

// Browser is what a `browser` step does: its action block, in source order.
//
// It is the third of the actions a Step may carry, beside Request and Exec, and
// Type is what decides which one is read. It is a struct with one slice rather
// than the slice itself so that a setting the whole block comes to take -- a
// base URL, a default selector timeout -- has somewhere to go that is not a
// second field on Step.
type Browser struct {
	// Acts are the actions to perform, in the order the scenario wrote them.
	// That order is load-bearing and is the only control flow a browser step
	// has: `fill` then `click` is a form submitted, and the reverse is not.
	Acts []Act
}

// Act is one statement of a browser block: `goto "/orders"`, `fill "#email" =
// "alice@example.com"`.
//
// Every field is a string and every one is already resolved. A selector is a
// selector: nothing downstream may re-interpret it, so `click "text=${plan}"`
// arrives here as `text=pro` and a selector that legitimately contains a brace
// is not a template. Which of the eight names this is, and whether that name
// takes a value, were settled by pkg/dsl/check before anything lowered -- so an
// executor switches on Name and may assume the arity it finds.
type Act struct {
	// Name is the action: goto, click, fill, select, press, hover, upload or
	// wait. It is the spelling the scenario wrote, which the checker has
	// already confirmed is one of the eight.
	Name string

	// Target is the action's first argument. A selector for six of them, a URL
	// for `goto`, and a duration string for `wait` -- the one action whose
	// argument is not something in the page. For `press` it is the key.
	Target string

	// Value is the second argument of the three actions that take one -- fill,
	// select and upload -- and empty for the other five. There is no "absent"
	// as against "empty": the checker rejects a value on an action that takes
	// none and demands one on an action that does, so Name is enough to know
	// whether this field means anything.
	Value string

	// Line is the line the action was written on, which is where a failure
	// inside the page points. A step's own line is the fallback the result tree
	// already has; this is finer, because a browser step is the one step type
	// whose action is many statements and "the step failed" would not say
	// which.
	Line int

	// File is the file Line is in, when that is not the scenario's own -- an
	// action a use brought in from a collection -- and empty otherwise.
	File string
}
