package lower

import (
	"strings"
	"testing"
	"time"

	"artemis/pkg/executor"
	"artemis/pkg/shared/models"
)

// bound lowers one scenario and binds its vars, which is the state a step is
// rendered against at run time. Every value a test wants in scope is declared in
// the source as a `var`, because the checker rejects a name nothing declares --
// so a test that injected one into the scope would be testing a file that does
// not compile.
func bound(t *testing.T, src string) (*Scenario, executor.Scope) {
	t.Helper()
	sc := one(t, src)
	scope := executor.NewScope()
	if err := sc.Bind(scope); err != nil {
		t.Fatalf("Bind() = %v, want nil", err)
	}
	return sc, scope
}

// modelOf is the models.Step an executor takes for the scenario's first step.
func modelOf(t *testing.T, src string) models.Step {
	t.Helper()
	sc, scope := bound(t, src)
	model, err := sc.Steps[0].Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v, want nil", err)
	}
	return model
}

// modelErr is the error the scenario's first step reports instead of a model.
func modelErr(t *testing.T, src string) error {
	t.Helper()
	sc, scope := bound(t, src)
	model, err := sc.Steps[0].Model(envOf(scope))
	if err == nil {
		t.Fatalf("Model() = %#v, want an error", model)
	}
	return err
}

// This is the runtime shape of an api step: the method, the URL with its query
// folded in, the headers, and the body as compact JSON. It is what httpstep
// reads and nothing else.
func TestAnAPIStepsRuntimeShape(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  var url = "https://api.test"
  var token = "abc"
  step "orders" {
    post "${url}/orders" {
      header "Content-Type" = "application/json"
      header "Authorization" = "Bearer ${token}"
      query "limit" = 10
      query "tag" = "new"
      body = {"name": "alice", "count": 2}
    }
  }
}`)

	if model.Type != "api" {
		t.Errorf("Type = %q, want api -- the key httpstep registers under", model.Type)
	}
	if model.Name != "orders" {
		t.Errorf("Name = %q, want orders", model.Name)
	}
	if model.Line == 0 {
		t.Error("Line = 0, want the step's line so a step that cannot run points at it")
	}
	if want := "POST"; model.Request.Method != want {
		t.Errorf("Method = %q, want %q -- net/http matches methods exactly", model.Request.Method, want)
	}
	if want := "https://api.test/orders?limit=10&tag=new"; model.Request.URL != want {
		t.Errorf("URL = %q, want %q", model.Request.URL, want)
	}
	if got := model.Request.Headers["Authorization"]; got != "Bearer abc" {
		t.Errorf("Authorization = %q, want the interpolated var", got)
	}
	if got := model.Request.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	// An object literal is serialised, not spliced: this removes the failure
	// mode where a captured object reached a body by text concatenation.
	if want := `{"count":2,"name":"alice"}`; model.Request.Body != want {
		t.Errorf("Body = %q, want the object as compact JSON %q", model.Request.Body, want)
	}
}

// A step with no request block is the common case and legal: a bare verb and a
// URL.
func TestABareRequestLowers(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  step "ping" {
    get "https://api.test/health"
  }
}`)

	if model.Request.URL != "https://api.test/health" {
		t.Errorf("URL = %q, want it unchanged", model.Request.URL)
	}
	if model.Request.Body != "" {
		t.Errorf("Body = %q, want empty for a step that wrote none", model.Request.Body)
	}
	if model.Request.Headers != nil {
		t.Errorf("Headers = %#v, want nil for a step that wrote none", model.Request.Headers)
	}
}

// A URL that already carries a query keeps it and the step's parameters are
// appended: dropping either half would be the worst of the three answers.
func TestQueryParametersJoinAURLThatHasSome(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"no query", "https://api.test/o", "https://api.test/o?limit=10"},
		{"a query already", "https://api.test/o?page=2", "https://api.test/o?page=2&limit=10"},
		{"a trailing question mark", "https://api.test/o?", "https://api.test/o?limit=10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			model := modelOf(t, `scenario "s" {
  step "orders" {
    get "`+c.url+`" {
      query "limit" = 10
    }
  }
}`)
			if model.Request.URL != c.want {
				t.Errorf("URL = %q, want %q", model.Request.URL, c.want)
			}
		})
	}
}

// Repeated parameters keep both values in the order they were written: source
// order, not sorted, because sorting would reorder what the author wrote and
// `tag=a&tag=b` is not `tag=b&tag=a` to a server.
func TestRepeatedQueryParametersKeepSourceOrder(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  step "search" {
    get "https://api.test/s" {
      query "tag" = "b"
      query "tag" = "a"
    }
  }
}`)

	if want := "https://api.test/s?tag=b&tag=a"; model.Request.URL != want {
		t.Errorf("URL = %q, want %q", model.Request.URL, want)
	}
}

// A query name and value are escaped, so a value with an ampersand in it cannot
// forge a second parameter.
func TestQueryParametersAreEscaped(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  step "search" {
    get "https://api.test/s" {
      query "q" = "a&b=c d"
    }
  }
}`)

	if want := "https://api.test/s?q=a%26b%3Dc+d"; model.Request.URL != want {
		t.Errorf("URL = %q, want %q", model.Request.URL, want)
	}
}

// This is the runtime shape of a terminal step: the models.Exec execstep reads.
func TestATerminalStepsRuntimeShape(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  var pw = "hunter2"
  var n = 3
  step "seed the database" {
    run "psql" {
      args  = ["-f", "seed.sql", "-n", n]
      cwd   = "db"
      stdin = "select 1;"
      env   { PGPASSWORD = pw, PGHOST = "localhost" }
    }
  }
}`)

	if model.Type != "terminal" {
		t.Errorf("Type = %q, want terminal -- the key execstep registers under", model.Type)
	}
	if model.Exec.Command != "psql" {
		t.Errorf("Command = %q, want psql", model.Exec.Command)
	}
	// A number renders as written, not as 3.000000, and nothing is word-split.
	want := []string{"-f", "seed.sql", "-n", "3"}
	if strings.Join(model.Exec.Args, "|") != strings.Join(want, "|") {
		t.Errorf("Args = %#v, want %#v", model.Exec.Args, want)
	}
	if model.Exec.Cwd != "db" {
		t.Errorf("Cwd = %q, want db", model.Exec.Cwd)
	}
	if model.Exec.Stdin != "select 1;" {
		t.Errorf("Stdin = %q, want the stdin it wrote", model.Exec.Stdin)
	}
	// An env setting's name is an identifier, not an expression: PGPASSWORD must
	// not be resolved as a variable, and its value must be.
	if got := model.Exec.Env["PGPASSWORD"]; got != "hunter2" {
		t.Errorf("env PGPASSWORD = %q, want the var's value", got)
	}
	if got := model.Exec.Env["PGHOST"]; got != "localhost" {
		t.Errorf("env PGHOST = %q, want localhost", got)
	}
	if len(model.Exec.Env) != 2 {
		t.Errorf("Env = %#v, want exactly the two settings written", model.Exec.Env)
	}
}

// A bare `run` with no block is legal: a command and nothing else.
func TestABareRunLowers(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  step "list" {
    run "ls"
  }
}`)

	if model.Exec.Command != "ls" {
		t.Errorf("Command = %q, want ls", model.Exec.Command)
	}
	if model.Exec.Args != nil || model.Exec.Env != nil {
		t.Errorf("Exec = %#v, want no args and no env", model.Exec)
	}
}

// A browser step is the node the registry will dispatch on, with its actions in
// source order and no models action filled: nothing executes "browser" until
// ART-47, and executor.Run reports an unregistered type as an error rather than
// as a pass.
func TestABrowserStepsRuntimeShape(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  step "upgrade" {
    browser {
      goto "/settings/billing"
      fill "#email" = "alice@example.com"
      click "text=Sign in"
    }
  }
}`)
	st := sc.Steps[0]

	if st.Type != "browser" {
		t.Errorf("Type = %q, want browser", st.Type)
	}
	if st.Request != nil || st.Run != nil {
		t.Errorf("a browser step filled an api or terminal action: %#v %#v", st.Request, st.Run)
	}
	wantActs := []struct {
		name     string
		hasValue bool
	}{{"goto", false}, {"fill", true}, {"click", false}}
	if len(st.Acts) != len(wantActs) {
		t.Fatalf("lowered %d acts, want %d", len(st.Acts), len(wantActs))
	}
	for i, want := range wantActs {
		if st.Acts[i].Name != want.name {
			t.Errorf("act %d = %q, want %q -- acts are in source order", i, st.Acts[i].Name, want.name)
		}
		if (st.Acts[i].Value != nil) != want.hasValue {
			t.Errorf("act %q value = %#v, want hasValue=%v", want.name, st.Acts[i].Value, want.hasValue)
		}
		if st.Acts[i].Target == nil {
			t.Errorf("act %q has no target", want.name)
		}
	}

	model, err := st.Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v, want nil", err)
	}
	if model.Type != "browser" {
		t.Errorf("Model().Type = %q, want browser", model.Type)
	}
	if model.Request.URL != "" || model.Exec.Command != "" {
		t.Errorf("Model() filled a request or an exec: %#v %#v", model.Request, model.Exec)
	}
}

// `retry` and `timeout` lower onto the fields the runner already reads them
// through, so the policy is decided by models.Retry and ART-16's AttemptTimeout
// and not by a second rule here.
func TestRetryAndTimeoutLowerOntoTheRunnersFields(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
    timeout = "5s"
    retry { times = 3, delay = "2s" }
  }
}`)

	if got := model.Retry.Attempts(); got != 3 {
		t.Errorf("Attempts() = %d, want 3", got)
	}
	wait, err := model.Retry.Wait()
	if err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
	if wait != 2*time.Second {
		t.Errorf("Wait() = %v, want 2s", wait)
	}
	got, err := model.AttemptTimeout(time.Minute)
	if err != nil {
		t.Fatalf("AttemptTimeout() = %v, want nil", err)
	}
	if got != 5*time.Second {
		t.Errorf("AttemptTimeout() = %v, want the step's 5s", got)
	}
}

// A policy whose values came from variables resolves the same way: `times` and
// `delay` are expressions like every other value position.
func TestAPolicyFromVariablesResolves(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  var tries = 2
  var gap = "150ms"
  step "orders" {
    get "https://api.test/o"
    retry { times = tries, delay = gap }
  }
}`)

	if got := model.Retry.Attempts(); got != 2 {
		t.Errorf("Attempts() = %d, want 2", got)
	}
	if wait, err := model.Retry.Wait(); err != nil || wait != 150*time.Millisecond {
		t.Errorf("Wait() = %v, %v; want 150ms", wait, err)
	}
}

// A step that writes no policy is one attempt, no wait, and the step type's own
// default deadline -- the same as a step that writes `retry { times = 1 }`.
func TestNoPolicyIsOneAttemptAndTheTypesDefault(t *testing.T) {
	model := modelOf(t, `scenario "s" {
  step "orders" {
    get "https://api.test/o"
  }
}`)

	if got := model.Retry.Attempts(); got != 1 {
		t.Errorf("Attempts() = %d, want 1 -- a step is never attempted zero times", got)
	}
	if wait, err := model.Retry.Wait(); err != nil || wait != 0 {
		t.Errorf("Wait() = %v, %v; want no wait", wait, err)
	}
	if got, err := model.AttemptTimeout(time.Minute); err != nil || got != time.Minute {
		t.Errorf("AttemptTimeout() = %v, %v; want the default handed in", got, err)
	}
}

// A retry count or a duration that came out of an expression is checked before
// anything runs. The checker catches the literal forms; these are the ones only
// the run could have known, and each stops the step rather than an attempt.
func TestABadPolicyFromAnExpressionIsAnError(t *testing.T) {
	cases := []struct {
		name   string
		vars   string
		policy string
		wantIn string
	}{
		{"times is not a whole number", `var n = 2.5`, `retry { times = n }`, "retry times is 2.5"},
		{"times is not a number at all", `var n = "three"`, `retry { times = n }`, "retry times is three"},
		{"a delay that is not a duration", `var d = "soon"`, `retry { delay = d }`, `"soon" is not a duration`},
		{"a timeout that is not a duration", `var d = "5 secs"`, `timeout = d`, `"5 secs" is not a duration`},
		{"a timeout that is not a string", `var d = 5`, `timeout = d`, "5 is not a duration"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := modelErr(t, `scenario "s" {
  `+c.vars+`
  step "orders" {
    get "https://api.test/o"
    `+c.policy+`
  }
}`)
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("Model() = %q, want it to mention %q", err, c.wantIn)
			}
			if !strings.Contains(err.Error(), "orders") {
				t.Errorf("Model() = %q, want it to name the step", err)
			}
		})
	}
}

// `args` must be an array. The checker rejects a literal that is not one; a var
// holding the wrong thing is caught here, with the value in the message, because
// there is no word splitting anywhere in a run block.
func TestArgsMustBeAnArray(t *testing.T) {
	err := modelErr(t, `scenario "s" {
  var a = "-f seed.sql"
  step "seed" {
    run "psql" {
      args = a
    }
  }
}`)

	if !strings.Contains(err.Error(), "want an array") {
		t.Errorf("Model() = %q, want it to say args must be an array", err)
	}
}

// An expression in a request that cannot be evaluated names the part of the
// request it was in, because a bad value in the URL and in a header are
// different mistakes to go and fix.
func TestAnUnevaluatableRequestNamesItsPart(t *testing.T) {
	cases := []struct {
		name   string
		block  string
		wantIn string
	}{
		{"the body", `body = {"n": -nope}`, "body"},
		{"a header value", `header "X" = -nope`, `header "X"`},
		{"a query value", `query "q" = -nope`, `query "q"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := modelErr(t, `scenario "s" {
  var nope = "a string"
  step "orders" {
    get "https://api.test/o" {
      `+c.block+`
    }
  }
}`)
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("Model() = %q, want it to name %q", err, c.wantIn)
			}
		})
	}
}

// The worked example's own steps produce runnable models: the end-to-end check
// that the design document's scenario lowers into something an executor would
// accept.
func TestTheWorkedExampleProducesModels(t *testing.T) {
	sc, scope := bound(t, readFixture(t, "checkout.art"))
	// The `orders` step reads the token the `login` step captures.
	scope.Set("token", "abc")

	env := envOf(scope)
	for _, st := range sc.Steps {
		model, err := st.Model(env)
		if err != nil {
			t.Fatalf("Model() for step %q = %v, want nil", st.Name, err)
		}
		if model.Name != st.Name || model.Type != st.Type {
			t.Errorf("model = %q/%q, want %q/%q", model.Name, model.Type, st.Name, st.Type)
		}
		if err := model.CheckTimeout(); err != nil {
			t.Errorf("step %q: CheckTimeout() = %v, want nil", st.Name, err)
		}
	}
}

// Every part of a run block names itself when it cannot be evaluated, for the
// same reason the request's parts do.
func TestAnUnevaluatableRunNamesItsPart(t *testing.T) {
	cases := []struct {
		name   string
		block  string
		wantIn string
	}{
		{"the command", `run -nope`, "command"},
		{"the cwd", `run "psql" { cwd = -nope }`, "cwd"},
		{"the stdin", `run "psql" { stdin = -nope }`, "stdin"},
		{"an env value", `run "psql" { env { PG = -nope } }`, "env PG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := modelErr(t, `scenario "s" {
  var nope = "a string"
  step "seed" {
    `+c.block+`
  }
}`)
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("Model() = %q, want it to name %q", err, c.wantIn)
			}
			if !strings.Contains(err.Error(), "seed") {
				t.Errorf("Model() = %q, want it to name the step", err)
			}
		})
	}
}

// A URL that cannot be evaluated names the URL.
func TestAnUnevaluatableURLNamesTheURL(t *testing.T) {
	err := modelErr(t, `scenario "s" {
  var nope = "a string"
  step "orders" {
    get "${-nope}/o"
  }
}`)
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("Model() = %q, want it to name the url", err)
	}
}
