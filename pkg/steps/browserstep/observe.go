package browserstep

import (
	"context"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// The roots a browser step binds, which is what `expect` and `capture` in a
// .art browser step may name.
//
// `page` is one root with a closed member set rather than two roots spelled
// `page.url` and `page.title`, because `page` is what an expression resolves
// and `url` is a member of it. observe_test.go compares these against
// check.Roots(Browser) and check.Members("page") so the checker's table and
// this file cannot drift.
const (
	// RootPage is the page the scenario is on, as an object.
	RootPage = "page"
	// MemberURL is page.url: the current address, after any redirect or
	// in-page navigation.
	MemberURL = "url"
	// MemberTitle is page.title: the document's title.
	MemberTitle = "title"
)

var _ executor.Executor = Executor{}

// Observe performs the step's actions in the scenario's page and reports what
// the page then looks like, leaving every assertion to the caller.
//
// Per pkg/executor's contract: one attempt, no retrying, no timing of its own
// beyond the step's deadline, and no assertions at all. The session is
// *borrowed* -- nothing here can close one, which is why a step that panicked
// cannot leak a browser process.
//
// The step is already rendered: pkg/dsl/lower evaluated every selector, value
// and URL in the block against the scenario's scope before this was called, so
// a selector that legitimately contains a brace is a selector. scope is unread
// for the same reason it is unread in httpstep.
//
// The roots come back as a *snapshot*, which is all a map[string]any can hold.
// An assertion that has to wait for the page to settle re-reads them through
// Bindings.Roots rather than through a second Observe -- re-observing would
// re-run the actions, which is the difference between `within` and `retry`.
func (e Executor) Observe(ctx context.Context, step models.Step, _ executor.Scope) (map[string]any, error) {
	timeout, err := step.AttemptTimeout(e.timeout())
	if err != nil {
		return nil, err
	}
	// Before the deadline is taken: opening a browser on the scenario's first
	// browser step costs a launch, and charging it to the step's own timeout
	// would make the first browser step of a scenario mysteriously slower to
	// time out than the rest.
	page, err := Open(ctx)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)

	// A run that was cancelled stops here rather than driving a page nobody is
	// waiting for. Playwright's calls take no context, so this is the one place
	// a browser step can notice -- which is enough, because the acts are
	// bounded by the step's own deadline anyway.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	logActs(step.Name, step.Browser.Acts)
	if err := perform(page.driver, step.Browser.Acts, step.Name, deadline); err != nil {
		return nil, err
	}
	return page.Roots()
}
