package browserstep

import (
	"context"
	"fmt"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"artemis/pkg/eval"
	"artemis/pkg/executor"
	"artemis/pkg/session"
)

// StepType is the type a scenario writes to get this executor. It is the key
// pkg/dsl/lower stamps on a step whose action block is `browser { ... }`.
const StepType = "browser"

// DefaultTimeout is how long one attempt of a browser step may take when the
// step does not say.
//
// 30s, the same as an api step's, and for the same reason: there is no "no
// timeout", because a step with no deadline is how a CI job hangs until someone
// notices. It is higher than it looks because it bounds the *whole* step --
// every act in the block together -- rather than one click, and because a cold
// first navigation in a headless browser is slower than any request.
const DefaultTimeout = 30 * time.Second

// Executor is the browser step.
//
// It holds no page and no browser. The page belongs to the scenario and lives
// in pkg/session; this is stateless, which is what lets one registered value
// serve every scenario in a run.
type Executor struct {
	// Timeout is the deadline for a step that does not set one. Zero means
	// DefaultTimeout.
	Timeout time.Duration
}

func init() {
	executor.Register(StepType, Executor{})
}

func (e Executor) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}

// Bindings is the live page an assertion reads: the two roots, re-read on every
// call, and the five element functions.
//
// It exists because a root is a *value*. Observe returns `page.url` and
// `page.title` as strings, which is the only thing a map[string]any can hold,
// and a settle loop re-asking `expect page.url contains "/done"` needs them
// read again rather than remembered. So the runner holds a Bindings for the
// duration of a browser step's assertions and asks it for fresh roots between
// tries; the element functions need no refreshing because each call already
// reads the DOM as it is now.
//
// It is the exported half of what Observe does internally, and deliberately the
// same lookup: a runner that found a different page from the one the acts ran
// against would be a bug nobody could see from a test run.
type Bindings struct{ driver driver }

// Elements is the page the browser scope's element functions read. It satisfies
// eval.Elements, which is how `text(".x")` in a .art file resolves.
func (b *Bindings) Elements() eval.Elements { return b.driver }

// Roots reads the `page` root as it is now.
//
// The shape is the one pkg/dsl/check declares: one root, `page`, with a closed
// member set. It is nested rather than two roots called `page.url` and
// `page.title` because `page` is what an expression resolves and `url` is a
// member of it -- see pkg/dsl/check/scope.go.
func (b *Bindings) Roots() (map[string]any, error) {
	url, err := b.driver.URL()
	if err != nil {
		return nil, err
	}
	title, err := b.driver.Title()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		RootPage: map[string]any{
			MemberURL:   url,
			MemberTitle: title,
		},
	}, nil
}

// Screenshot writes a PNG of the page to path, for a step that did not pass.
func (b *Bindings) Screenshot(path string) error { return b.driver.Screenshot(path) }

// Open returns the live bindings for the scenario's page on ctx.
//
// This is the lookup pkg/executor/sessions.go and pkg/session/doc.go between
// them specify, and it is in one place so that the step and the runner cannot
// reach two different pages: SessionsOf, then Session -- which opens the
// browser on the scenario's first browser step and returns the same one after
// -- then the cast to *session.Session.
//
// A context with no registry is an error naming the one cause: nothing but the
// runner's scenario boundary puts one there, so a browser step that cannot find
// one is a browser step outside a scenario.
func Open(ctx context.Context) (*Bindings, error) {
	sessions, ok := executor.SessionsOf(ctx)
	if !ok {
		return nil, fmt.Errorf("a browser step needs a browser session and this run has none " +
			"(the scenario boundary is what opens one -- see session.WithScenario)")
	}
	s, err := sessions.Session(ctx)
	if err != nil {
		return nil, err
	}
	return bindingsOf(s)
}

// bindingsOf is the cast, kept apart from Open so that the error for a session
// of the wrong type reads as the artemis bug it would be rather than as
// something a scenario did.
//
// The cast is the price pkg/executor/sessions.go names: Sessions.Session
// returns `any` precisely so that pkg/executor -- which the api and terminal
// steps also go through -- does not acquire a dependency on Playwright. It is
// paid here, once, by the one step type that needs a page.
func bindingsOf(s any) (*Bindings, error) {
	switch v := s.(type) {
	case *session.Session:
		return &Bindings{driver: newPage(v.Page())}, nil
	case driver:
		// A session that is already a driver, which is how the tests in this
		// package run the whole executor with no browser.
		return &Bindings{driver: v}, nil
	case playwright.Page:
		return &Bindings{driver: newPage(v)}, nil
	default:
		return nil, fmt.Errorf("the scenario's session is a %T, which is not a browser page", s)
	}
}
