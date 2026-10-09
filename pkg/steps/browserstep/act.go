package browserstep

import (
	"fmt"
	"time"

	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
)

// The eight actions, as the names a scenario writes. They are constants here
// and compared against check.BrowserActs() in act_test.go rather than imported
// from pkg/dsl/check: a step package that imported the checker would make the
// runtime depend on the front end, which is the dependency pkg/executor exists
// to avoid. The test is what keeps the two lists equal, and an action the
// checker admits and this file does not perform would be a .art file that
// compiles and then fails at run time for no reason a reader could find.
const (
	ActGoto   = "goto"
	ActClick  = "click"
	ActFill   = "fill"
	ActSelect = "select"
	ActPress  = "press"
	ActHover  = "hover"
	ActUpload = "upload"
	ActWait   = "wait"
)

// perform runs every act in source order against d, bounded by deadline.
//
// Source order is the only control flow a browser step has: `fill` then `click`
// is a form submitted and the reverse is not. It stops at the first act that
// fails, because the acts after it were acting on a page that never reached the
// state they assume.
//
// The deadline is the step's own `timeout`, and it is applied by setting the
// driver's timeout to what is *left* of it before each act. That is what makes
// `timeout = "5s"` mean five seconds for the step rather than five seconds for
// each of nine clicks: Playwright's timeout is per call, so a step-wide
// deadline has to be re-derived per call. The auto-waiting itself is untouched
// -- each act still waits for its element the way the engine does -- it is just
// not allowed to wait past the step's deadline.
func perform(d driver, acts []models.Act, name string, deadline time.Time) error {
	for i, a := range acts {
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("ran out of time after %s: %s on %s had none left",
				plural(i, "action", "actions"), a.Name, where(a))
		}
		d.SetTimeout(left)
		if err := act(d, a); err != nil {
			// The line, because a browser step's action is many statements and
			// the step's own line would point at the `step` keyword.
			return fmt.Errorf("%s: %w", where(a), err)
		}
	}
	return nil
}

// act performs one.
//
// Eight arms for eight actions, and a default that cannot be reached from a
// .art file: pkg/dsl/check rejects any other name with a did-you-mean before
// anything is lowered. It is here because a models.Step can also be built in
// Go or decoded from a tree (`artemis ast --from-json`), and an action silently
// doing nothing is the one outcome worth refusing -- a scenario would pass
// having never clicked anything.
//
// Neither argument is re-interpreted. pkg/dsl/lower evaluated both before this
// was called, so a selector that contains a brace is a selector and `click
// "text=${plan}"` has already become `text=pro`.
func act(d driver, a models.Act) error {
	switch a.Name {
	case ActGoto:
		return d.Goto(a.Target)
	case ActClick:
		return d.Click(a.Target)
	case ActHover:
		return d.Hover(a.Target)
	case ActFill:
		return d.Fill(a.Target, a.Value)
	case ActSelect:
		return d.Select(a.Target, a.Value)
	case ActUpload:
		return d.Upload(a.Target, a.Value)
	case ActPress:
		return d.Press(a.Target)
	case ActWait:
		return wait(d, a.Target)
	default:
		return fmt.Errorf("%q is not a browser action (artemis performs %s)",
			a.Name, listActs())
	}
}

// wait pauses for the duration the act names.
//
// `wait` is the one action whose argument is not something in the page, and the
// one that needs parsing here: pkg/dsl/check has already rejected a literal
// that is not a duration, but `wait "${pause}"` is skipped there as every
// interpolated budget is, so a bad one arrives at run time and must say so
// rather than pausing for zero.
//
// A negative duration is an error for the same reason `timeout` refuses one: it
// is the scenario's mistake, and treating it as "no wait" would make the
// mistake invisible.
func wait(d driver, budget string) error {
	pause, err := time.ParseDuration(budget)
	if err != nil {
		return fmt.Errorf("wait %q is not a duration (want something like \"500ms\" or \"2s\")", budget)
	}
	if pause < 0 {
		return fmt.Errorf("wait %q is negative", budget)
	}
	if pause == 0 {
		return nil
	}
	d.Wait(pause)
	return nil
}

// listActs is the eight, in SPEC.md's table order, for the one error message
// that has to name them. The order is the table's rather than sorted so a
// reader who has the spec open finds them where they expect.
func listActs() string {
	return ActGoto + ", " + ActClick + ", " + ActFill + ", " + ActSelect + ", " +
		ActPress + ", " + ActHover + ", " + ActUpload + ", " + ActWait
}

// plural is the "1 action" / "3 actions" of the ran-out-of-time message. The
// runner has its own copy for its own messages; duplicating four lines is
// cheaper than a shared package for them.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// logActs records what the step is about to do, at the same level and in the
// same shape as httpstep's "API call" and execstep's command line, so a run's
// JSON log reads the same whatever the step type.
func logActs(name string, acts []models.Act) {
	for _, a := range acts {
		logger.Logger.Info("Browser action",
			"name", name, "action", a.Name, "target", a.Target, "line", a.Line)
	}
}

// where is the line an action was written on, as a failure names it: "line 7",
// or "auth.art:7" for an action a use brought in from a collection, whose line
// is not a line of the scenario's file.
func where(a models.Act) string {
	if a.File != "" {
		return fmt.Sprintf("%s:%d", a.File, a.Line)
	}
	return fmt.Sprintf("line %d", a.Line)
}
