package browserstep

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"artemis/pkg/dsl/check"
	"artemis/pkg/shared/models"
)

// far is a deadline no act in these tests gets near, so a test of what an
// action does is not also a test of the step's timeout.
func far() time.Time { return time.Now().Add(time.Hour) }

func acts(as ...models.Act) []models.Act { return as }

// Each of the eight maps to exactly one driver call with the arguments the
// scenario wrote, and the block runs in source order. That order is the only
// control flow a browser step has: `fill` then `click` is a form submitted and
// the reverse is not.
func TestTheEightActionsMapOntoTheDriver(t *testing.T) {
	f := newFake()
	block := acts(
		models.Act{Name: ActGoto, Target: "http://127.0.0.1:1/", Line: 3},
		models.Act{Name: ActFill, Target: "#user", Value: "alice", Line: 4},
		models.Act{Name: ActSelect, Target: "#plan", Value: "pro", Line: 5},
		models.Act{Name: ActUpload, Target: "#avatar", Value: "me.png", Line: 6},
		models.Act{Name: ActHover, Target: ".menu", Line: 7},
		models.Act{Name: ActPress, Target: "Enter", Line: 8},
		models.Act{Name: ActClick, Target: "text=Sign in", Line: 9},
		models.Act{Name: ActWait, Target: "250ms", Line: 10},
	)
	if err := perform(f, block, "upgrade", far()); err != nil {
		t.Fatalf("perform() = %v, want nil", err)
	}

	want := []string{
		"goto http://127.0.0.1:1/",
		"fill #user = alice",
		"select #plan = pro",
		"upload #avatar = me.png",
		"hover .menu",
		"press Enter",
		"click text=Sign in",
		"wait 250ms",
	}
	if got := f.log(); got != strings.Join(want, "\n") {
		t.Errorf("the calls were\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	// And `wait` waited for what it said, rather than for a default or for
	// nothing at all.
	if len(f.waited) != 1 || f.waited[0] != 250*time.Millisecond {
		t.Errorf("waited %v, want one pause of 250ms", f.waited)
	}
}

// The actions this file performs are exactly the actions pkg/dsl/check admits.
// An action the checker accepts and this file does not perform would be a file
// that compiles, formats, encodes and then fails at run time; one this file
// performs and the checker rejects is dead code.
func TestTheActionsAreExactlyTheCheckersActions(t *testing.T) {
	mine := map[string]bool{}
	for _, name := range []string{ActGoto, ActClick, ActFill, ActSelect, ActPress, ActHover, ActUpload, ActWait} {
		mine[name] = true
	}

	theirs := map[string]bool{}
	for _, a := range check.BrowserActs() {
		theirs[a.Name] = true
		if !mine[a.Name] {
			t.Errorf("pkg/dsl/check admits the action %q and browserstep cannot perform it", a.Name)
		}
	}
	for name := range mine {
		if !theirs[name] {
			t.Errorf("browserstep performs %q and pkg/dsl/check does not admit it", name)
		}
	}

	// And the arity each side believes in. The checker decides whether an act
	// may carry a value; act() reads Value for exactly the ones that may.
	withValue := map[string]bool{ActFill: true, ActSelect: true, ActUpload: true}
	for _, a := range check.BrowserActs() {
		if a.TakesValue != withValue[a.Name] {
			t.Errorf("check.BrowserActs says %q takes a value = %v; browserstep believes %v",
				a.Name, a.TakesValue, withValue[a.Name])
		}
	}
}

// A name that is not one of the eight is refused rather than silently skipped.
// It cannot come from a .art file -- the checker rejects it with a did-you-mean
// -- but it can come from a tree (`artemis ast --from-json`) or from Go, and a
// scenario that passed having never clicked anything is the one outcome worth
// refusing.
func TestAnUnknownActionIsRefused(t *testing.T) {
	f := newFake()
	err := perform(f, acts(models.Act{Name: "scroll", Target: ".foot", Line: 7}), "s", far())
	if err == nil {
		t.Fatal("perform() = nil, want an error")
	}
	for _, want := range []string{"scroll", "line 7", "click", "upload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("perform() = %v, want it to mention %q", err, want)
		}
	}
	if f.log() != "" {
		t.Errorf("an unknown action still called the driver: %s", f.log())
	}
}

// The block stops at the first act that fails: the acts after it were acting on
// a page that never reached the state they assume, and running them would turn
// one failure into a cascade of confusing ones.
func TestABlockStopsAtTheFirstFailure(t *testing.T) {
	boom := errors.New("element is not visible")
	f := newFake().breaks("Click", boom)

	err := perform(f, acts(
		models.Act{Name: ActGoto, Target: "http://127.0.0.1:1/", Line: 3},
		models.Act{Name: ActClick, Target: "#go", Line: 4},
		models.Act{Name: ActFill, Target: "#user", Value: "alice", Line: 5},
	), "s", far())

	if err == nil {
		t.Fatal("perform() = nil, want the click's error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("perform() = %v, want it to wrap the driver's error", err)
	}
	// The line, because a browser step's action is many statements and the
	// step's own line points at the `step` keyword.
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("perform() = %v, want it to name line 4", err)
	}
	if strings.Contains(f.log(), "fill") {
		t.Errorf("the act after the failure still ran:\n%s", f.log())
	}
}

// The step's timeout bounds the whole block, not each act.
//
// Playwright's own timeout is per call, so a step-wide deadline has to be
// re-derived before every act -- otherwise `timeout = "5s"` would mean five
// seconds for each of nine clicks. Each SetTimeout must therefore be smaller
// than the one before it.
func TestEachActIsGivenWhatIsLeftOfTheStepsDeadline(t *testing.T) {
	f := newFake()
	deadline := time.Now().Add(2 * time.Second)

	if err := perform(f, acts(
		models.Act{Name: ActWait, Target: "1ms", Line: 3},
		models.Act{Name: ActWait, Target: "1ms", Line: 4},
		models.Act{Name: ActWait, Target: "1ms", Line: 5},
	), "s", deadline); err != nil {
		t.Fatalf("perform() = %v, want nil", err)
	}

	if len(f.timeouts) != 3 {
		t.Fatalf("SetTimeout was called %d times, want one per act", len(f.timeouts))
	}
	for i := 1; i < len(f.timeouts); i++ {
		if f.timeouts[i] >= f.timeouts[i-1] {
			t.Errorf("act %d was given %v and act %d was given %v: the budget must shrink",
				i-1, f.timeouts[i-1], i, f.timeouts[i])
		}
	}
	if f.timeouts[0] > 2*time.Second {
		t.Errorf("the first act was given %v, more than the step's 2s", f.timeouts[0])
	}
}

// A deadline that has already passed stops the block rather than handing the
// engine a zero or negative timeout, which Playwright reads as "no timeout" --
// the one thing a step with an expired deadline must not get.
func TestAnExpiredDeadlineStopsTheBlock(t *testing.T) {
	f := newFake()
	err := perform(f, acts(
		models.Act{Name: ActClick, Target: "#a", Line: 3},
	), "s", time.Now().Add(-time.Second))

	if err == nil {
		t.Fatal("perform() = nil, want a ran-out-of-time error")
	}
	if !strings.Contains(err.Error(), "ran out of time") {
		t.Errorf("perform() = %v, want it to say the step ran out of time", err)
	}
	if f.log() != "" {
		t.Errorf("an expired deadline still drove the page: %s", f.log())
	}
}

// The ran-out-of-time message says how far the block got and which act was
// next, because "the step timed out" on a block of nine says nothing about
// which of the nine to look at.
func TestTheRanOutOfTimeMessageSaysHowFarItGot(t *testing.T) {
	f := newFake()
	// Two acts that consume the budget, then one that cannot start. The waits
	// are recorded rather than slept, so the deadline is what expires: it is
	// already in the past for the third act because the second pushed past it.
	err := perform(f, acts(
		models.Act{Name: ActWait, Target: "1ms", Line: 3},
		models.Act{Name: ActWait, Target: "1ms", Line: 4},
		models.Act{Name: ActClick, Target: "#go", Line: 5},
	), "s", time.Now().Add(time.Millisecond))
	// A millisecond is enough for two recorded waits on any machine but not
	// reliably gone by the third act, so this asserts the message only when
	// the deadline did expire.
	if err == nil {
		t.Skip("the deadline had not expired by the third act on this machine")
	}
	if !strings.Contains(err.Error(), "actions") && !strings.Contains(err.Error(), "action") {
		t.Errorf("perform() = %v, want it to say how many actions ran", err)
	}
}

// `wait` is the one action whose argument is a duration, and the one that can
// still be wrong at run time: the checker rejects a bad literal, but `wait
// "${pause}"` is skipped there as every interpolated budget is.
func TestWaitRefusesABadDuration(t *testing.T) {
	for _, tc := range []struct{ budget, want string }{
		{"soon", "not a duration"},
		{"-2s", "negative"},
	} {
		f := newFake()
		err := perform(f, acts(models.Act{Name: ActWait, Target: tc.budget, Line: 3}), "s", far())
		if err == nil {
			t.Fatalf("wait %q = nil, want an error", tc.budget)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("wait %q = %v, want it to say %q", tc.budget, err, tc.want)
		}
		if len(f.waited) != 0 {
			t.Errorf("wait %q still paused for %v", tc.budget, f.waited)
		}
	}
}

// `wait "0s"` is a no-op rather than a call into the engine. It is what a
// scenario with an interpolated budget of zero writes, and asking a browser to
// wait for no time is a round trip for nothing.
func TestWaitOfZeroDoesNothing(t *testing.T) {
	f := newFake()
	if err := perform(f, acts(models.Act{Name: ActWait, Target: "0s", Line: 3}), "s", far()); err != nil {
		t.Fatalf("perform() = %v, want nil", err)
	}
	if len(f.waited) != 0 {
		t.Errorf("wait \"0s\" paused for %v, want no pause at all", f.waited)
	}
}

// An empty block drives nothing and is not an error. A .art file cannot hold
// one -- the block is required -- but a tree built by hand can, and the step
// should then be a step that did nothing rather than a crash.
func TestAnEmptyBlockIsNotAnError(t *testing.T) {
	f := newFake()
	if err := perform(f, nil, "s", far()); err != nil {
		t.Fatalf("perform(nil) = %v, want nil", err)
	}
	if f.log() != "" {
		t.Errorf("an empty block called the driver: %s", f.log())
	}
}

// Neither argument is re-interpreted. pkg/dsl/lower evaluated both before the
// executor saw them, so a selector that legitimately holds a brace, a dollar or
// a quote reaches the engine byte for byte.
func TestAnArgumentIsPassedThroughByteForByte(t *testing.T) {
	odd := `[data-tpl="{{not a template}}"][title='it''s ${here}']`
	f := newFake()
	if err := perform(f, acts(models.Act{Name: ActClick, Target: odd, Line: 3}), "s", far()); err != nil {
		t.Fatalf("perform() = %v, want nil", err)
	}
	if got := f.log(); got != "click "+odd {
		t.Errorf("the selector reached the driver as\n%s\nwant\n%s", got, "click "+odd)
	}
}

// The eight are named in SPEC.md's table order in the one message that lists
// them, so a reader with the spec open finds them where they expect.
func TestTheActionListIsInTheSpecsOrder(t *testing.T) {
	want := "goto, click, fill, select, press, hover, upload, wait"
	if got := listActs(); got != want {
		t.Errorf("listActs() = %q, want %q", got, want)
	}
}

func TestPluralReadsAsEnglish(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0 actions"}, {1, "1 action"}, {2, "2 actions"}} {
		if got := plural(tc.n, "action", "actions"); got != tc.want {
			t.Errorf("plural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// A sanity check on the fake itself: a settling element answers `before` until
// the Nth read and `after` from then on. Every settle-loop test in pkg/cli
// stands on this, so it is asserted rather than assumed.
func TestTheFakeSettlesAfterNReads(t *testing.T) {
	f := newFake().settles("#s", 2,
		element{text: "Working"}, element{text: "Pro"})

	var got []string
	for i := 0; i < 4; i++ {
		v, err := f.Text("#s")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprint(v))
	}
	want := []string{"Working", "Working", "Pro", "Pro"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("reads were %v, want %v", got, want)
	}
}
