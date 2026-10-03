package browserstep

import (
	"time"

	"artemis/pkg/eval"
)

// driver is everything this package needs of a page, in plain Go values.
//
// It is unexported and it has exactly two implementations: pwPage, which is
// playwright-go, and a fake in the tests. Exporting it would make "what may
// drive a browser step" an open question; the answer is this package, and a
// caller who wants a different engine wants a different implementation of this
// file rather than a parameter.
//
// The five element functions are not restated here: the interface embeds
// eval.Elements, which already declares them with exactly these signatures and
// already documents what each returns. Two copies of five method signatures
// would be two things that can drift, and the evaluator's is the one that
// matters -- it is what `text(".x")` in a .art file resolves through.
//
// The actions are one method each rather than one Act-shaped method, because
// each takes a different number of strings and a switch with eight arms over
// eight methods is the plainest thing a reader can check against SPEC.md's
// table of eight.
type driver interface {
	eval.Elements

	// Goto navigates. A relative target resolves against the page's current
	// address, which is SPEC.md's rule and is why this takes the resolving on
	// rather than handing the string straight to the engine.
	Goto(target string) error

	// Click, Hover: a selector, and the engine's own auto-wait.
	Click(selector string) error
	Hover(selector string) error

	// Fill, Select, Upload: the three actions that take a value. Select
	// chooses by the option's value, and Upload's value is a file path on the
	// machine artemis is running on.
	Fill(selector, value string) error
	Select(selector, value string) error
	Upload(selector, path string) error

	// Press sends one key to whatever has focus, so it takes a key and no
	// selector -- the one action of the eight whose argument is neither a
	// selector nor a duration.
	Press(key string) error

	// Wait pauses for a fixed duration. It is the only sleeping this package
	// does, and it is here because a scenario asked for it by name: waiting
	// for a *condition* is `within` on the assertion that names the condition.
	Wait(d time.Duration)

	// SetTimeout bounds each later call. The act loop sets it to what is left
	// of the step's own `timeout` before every action, so a step's deadline
	// bounds the auto-waiting rather than being bounded by it.
	SetTimeout(d time.Duration)

	// URL and Title are the two members of the `page` root. URL cannot fail in
	// any engine today; it returns an error so that one which has to ask the
	// browser is not a signature change through the package.
	URL() (string, error)
	Title() (string, error)

	// Screenshot writes a PNG of the page to path. The directory is the
	// caller's to create.
	Screenshot(path string) error
}
