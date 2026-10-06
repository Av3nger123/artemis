package lower

import (
	"slices"
	"testing"

	"artemis/pkg/eval"
	"artemis/pkg/shared/models"
)

// secretsOf is the marks of the nth step.
//
// They are computed in Model and not at lowering time, because a header's name
// is itself an expression, so every test here goes through Model.
func secretsOf(t *testing.T, src string, n int) models.Secrets {
	t.Helper()
	sc, scope := bound(t, src)
	m, err := sc.Steps[n].Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v, want nil", err)
	}
	return m.Secrets
}

func TestSecretsPerRequestField(t *testing.T) {
	t.Setenv("P", "hunter2")
	t.Setenv("U", "https://api.test")
	got := secretsOf(t, `scenario "s" {
  secret var pw = env("P")
  var u = env("U")
  step "one" {
    post "${u}/token" {
      header "X-Plain" = "v"
      header "Authorization" = "Bearer ${pw}"
      body = {"user": "alice", "pass": pw, "deep": {"k": pw}}
    }
    expect status == 200
  }
}`, 0)

	if got.URL {
		t.Error("URL must not be secret: neither it nor a query read a secret")
	}
	if got.Headers["X-Plain"] {
		t.Error("X-Plain must not be secret")
	}
	if !got.Headers["Authorization"] {
		t.Error("Authorization must be secret: it interpolates pw")
	}
	if got.Body {
		t.Error("an object-literal body gets paths, not a whole-body mark")
	}
	want := []string{"/deep/k", "/pass"}
	paths := slices.Clone(got.BodyPaths)
	slices.Sort(paths)
	if !slices.Equal(paths, want) {
		t.Errorf("BodyPaths = %v, want %v", paths, want)
	}
}

// A secret query value marks the URL, because Model folds the query into it.
func TestSecretsQueryMarksTheURL(t *testing.T) {
	t.Setenv("P", "hunter2")
	got := secretsOf(t, `scenario "s" {
  secret var pw = env("P")
  step "one" {
    get "/x" { query "t" = pw }
    expect status == 200
  }
}`, 0)
	if !got.URL {
		t.Error("a secret query value must mark the URL: it is folded in there")
	}
}

// A body that is not a literal has no field to walk, so all of it goes.
func TestSecretsWholeBodyWhenNotALiteral(t *testing.T) {
	t.Setenv("P", `{"a":1}`)
	got := secretsOf(t, `scenario "s" {
  secret var payload = env("P")
  step "one" {
    post "/x" { body = payload }
    expect status == 200
  }
}`, 0)
	if !got.Body {
		t.Error("a body that is not a literal must be withheld whole")
	}
	if len(got.BodyPaths) != 0 {
		t.Errorf("BodyPaths = %v, want none", got.BodyPaths)
	}
}

// A secret under a key no pointer can name withholds the whole body, rather
// than emitting a pointer that would match nothing.
func TestSecretsUnnameableKeyWithholdsTheBody(t *testing.T) {
	t.Setenv("P", "hunter2")
	t.Setenv("K", "pass")
	got := secretsOf(t, `scenario "s" {
  secret var pw = env("P")
  var k = env("K")
  step "one" {
    post "/x" { body = {"${k}": pw} }
    expect status == 200
  }
}`, 0)
	if !got.Body {
		t.Error("an interpolated key holding a secret must withhold the whole body")
	}
	if len(got.BodyPaths) != 0 {
		t.Errorf("BodyPaths = %v, want none", got.BodyPaths)
	}
}

// A scenario with no secret must mark nothing at all, which is what keeps every
// existing report byte-identical.
func TestSecretsNothingMarkedWithoutTheModifier(t *testing.T) {
	t.Setenv("P", "hunter2")
	sc, scope := bound(t, `scenario "s" {
  var pw = env("P")
  step "one" {
    post "/token" {
      header "Authorization" = "Bearer ${pw}"
      body = {"pass": pw}
    }
    expect status == 200
  }
}`)
	m, err := sc.Steps[0].Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v, want nil", err)
	}
	if m.Secrets.Any() {
		t.Errorf("Secrets.Any() = true for a scenario with no secret: %+v", m.Secrets)
	}
}

// A secret capture is in scope for the steps below its own and not for its own,
// which is SPEC.md's scoping rule applied to the set.
func TestSecretsCaptureReachesTheNextStep(t *testing.T) {
	sc, scope := bound(t, `scenario "s" {
  step "one" {
    post "/token" { body = {"u": "alice"} }
    expect status == 200
    secret capture token = body.data.access_token
  }
  step "two" {
    get "/orders" { header "Authorization" = "Bearer ${token}" }
    expect status == 200
  }
}`)
	scope.Set("token", "a-token")
	env := envOf(scope)

	first, err := sc.Steps[0].Model(env)
	if err != nil {
		t.Fatalf("Model() = %v", err)
	}
	// Nothing it *sent* is secret: the scenario declares no secret var, so the
	// request marks are all empty. Its observation is another matter -- the
	// token is in its own response body, which ART-55's marks withhold.
	if first.Secrets.URL || first.Secrets.Body || len(first.Secrets.Headers) > 0 ||
		len(first.Secrets.BodyPaths) > 0 {
		t.Errorf("the capturing step's request marks nothing: %+v", first.Secrets)
	}
	if got := first.Secrets.ObservedPaths["body"]; len(got) != 1 || got[0] != "/data/access_token" {
		t.Errorf("ObservedPaths[body] = %v, want [/data/access_token]", got)
	}

	second, err := sc.Steps[1].Model(env)
	if err != nil {
		t.Fatalf("Model() = %v", err)
	}
	if !second.Secrets.Headers["Authorization"] {
		t.Error("the next step's Authorization header must be secret")
	}
}

// A capture is secret when its own expression reads a secret binding, with no
// modifier on it: a value derived from a credential is the credential's to leak.
func TestSecretsCaptureInferredFromItsExpression(t *testing.T) {
	t.Setenv("P", "hunter2")
	sc, scope := bound(t, `scenario "s" {
  secret var pw = env("P")
  step "one" {
    get "/x"
    expect status == 200
    capture part = match(pw, /^(.{3})/)
  }
  step "two" {
    get "/y" { header "X-Part" = "${part}" }
    expect status == 200
  }
}`)
	scope.Set("part", "hun")
	m, err := sc.Steps[1].Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v", err)
	}
	if !m.Secrets.Headers["X-Part"] {
		t.Error("a capture that reads a secret binding is itself secret")
	}
}

// A terminal step's env setting is the case SPEC.md:35 shows.
func TestSecretsTerminalEnvAndStdin(t *testing.T) {
	t.Setenv("P", "hunter2")
	sc, scope := bound(t, `scenario "s" {
  secret var pw = env("P")
  step "one" {
    run "psql" {
      args  = ["-f", "seed.sql"]
      stdin = pw
      env   { PGPASSWORD = pw, PGHOST = "localhost" }
    }
    expect exit_code == 0
  }
}`)
	m, err := sc.Steps[0].Model(envOf(scope))
	if err != nil {
		t.Fatalf("Model() = %v", err)
	}
	if !m.Secrets.Env["PGPASSWORD"] {
		t.Error("PGPASSWORD must be secret")
	}
	if m.Secrets.Env["PGHOST"] {
		t.Error("PGHOST must not be secret")
	}
	if !m.Secrets.Stdin {
		t.Error("stdin must be secret")
	}
	if m.Secrets.Args {
		t.Error("args must not be secret")
	}
}

// operandMarks has to agree with how eval.Assert fills an Outcome: for a
// comparison Actual is the left operand and Expected the right. A disagreement
// here would redact the wrong half of a failure message.
func TestSecretsAssertionOperands(t *testing.T) {
	t.Setenv("P", "hunter2")
	sc := one(t, `scenario "s" {
  secret var pw = env("P")
  step "one" {
    get "/x"
    expect body.given == pw
    expect pw == body.given
    expect status == 200
    expect pw exists
    expect not pw == "x"
    expect body.token == "plain"
  }
}`)
	tests := []struct {
		n                        int
		wantExpected, wantActual bool
		why                      string
	}{
		{0, true, false, `body.given == pw: the right operand is secret`},
		{1, false, true, `pw == body.given: the left operand is secret`},
		{2, false, false, `status == 200: neither side is secret`},
		{3, false, true, `pw exists: one operand, and it is the subject`},
		{4, false, true, `not pw == "x": parses as not (pw == "x"), so pw is the left operand`},
		{5, false, false, `body.token == "plain": a member name is not a binding`},
	}
	for _, tc := range tests {
		e := sc.Steps[0].Expects[tc.n]
		if e.ExpectedSecret != tc.wantExpected || e.ActualSecret != tc.wantActual {
			t.Errorf("%s: expected=%v actual=%v, want expected=%v actual=%v",
				tc.why, e.ExpectedSecret, e.ActualSecret, tc.wantExpected, tc.wantActual)
		}
	}
}

// The marks have to reach the recorded assertion, which is the only reason they
// exist. Assert is the seam.
func TestSecretsReachTheRecordedAssertion(t *testing.T) {
	t.Setenv("P", "hunter2")
	sc, scope := bound(t, `scenario "s" {
  secret var pw = env("P")
  step "one" {
    get "/x"
    expect pw == "something else"
  }
}`)
	env := &eval.Env{Vars: scope.Vars(), Lookup: func(string) (string, bool) { return "", false }}
	got := sc.Steps[0].Expects[0].Assert("one", env)
	if !got.ActualSecret {
		t.Error("ActualSecret did not reach the recorded assertion")
	}
	if got.Actual != "hunter2" {
		t.Errorf("Actual = %v: the tree must keep the true value", got.Actual)
	}
}

// A `secret capture` marks every later use of the binding, but the credential's
// origin is this step's response. These are the marks that let a trace withhold
// it there.
func TestSecretsObservedPaths(t *testing.T) {
	tests := []struct {
		name      string
		capture   string
		wantRoots []string
		wantPaths map[string][]string
	}{
		{
			name:      "a path is addressable, so the rest of the body survives",
			capture:   `secret capture token = body.data.access_token`,
			wantPaths: map[string][]string{"body": {"/data/access_token"}},
		},
		{
			name:      "an array index becomes a decimal pointer segment",
			capture:   `secret capture first = body.items[0].secret`,
			wantPaths: map[string][]string{"body": {"/items/0/secret"}},
		},
		{
			name:      "a quoted index is a pointer segment too",
			capture:   `secret capture ct = headers["set-cookie"]`,
			wantPaths: map[string][]string{"headers": {"/set-cookie"}},
		},
		{
			name:      "a call names no location, so the root goes whole",
			capture:   `secret capture tok = match(raw, /tok=(\w+)/)`,
			wantRoots: []string{"raw"},
		},
		{
			name:      "capturing a whole root withholds it whole",
			capture:   `secret capture all = body`,
			wantRoots: []string{"body"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := secretsOf(t, `scenario "s" {
  step "one" {
    get "/x"
    expect status == 200
    `+tc.capture+`
  }
}`, 0)
			for _, want := range tc.wantRoots {
				if !got.ObservedRoots[want] {
					t.Errorf("ObservedRoots missing %q: %+v", want, got.ObservedRoots)
				}
			}
			for root, wantPointers := range tc.wantPaths {
				if !slices.Equal(got.ObservedPaths[root], wantPointers) {
					t.Errorf("ObservedPaths[%q] = %v, want %v", root, got.ObservedPaths[root], wantPointers)
				}
			}
			if len(tc.wantPaths) == 0 && len(got.ObservedPaths) != 0 {
				t.Errorf("ObservedPaths = %v, want none", got.ObservedPaths)
			}
		})
	}
}

// A plain capture marks nothing of the observation, which is what keeps a trace
// of an ordinary scenario complete.
func TestSecretsObservedNothingForAPlainCapture(t *testing.T) {
	got := secretsOf(t, `scenario "s" {
  step "one" {
    get "/x"
    expect status == 200
    capture token = body.data.access_token
  }
}`, 0)
	if len(got.ObservedRoots) != 0 || len(got.ObservedPaths) != 0 {
		t.Errorf("a plain capture marked the observation: %+v", got)
	}
}

// A root withheld whole must not also list paths: keeping both would suggest
// the rest of that root survived.
func TestSecretsObservedWholeRootDropsItsPaths(t *testing.T) {
	got := secretsOf(t, `scenario "s" {
  step "one" {
    get "/x"
    expect status == 200
    secret capture a = body.data.token
    secret capture b = match(body, /x/)
  }
}`, 0)
	if !got.ObservedRoots["body"] {
		t.Fatalf("body must be withheld whole: %+v", got)
	}
	if _, found := got.ObservedPaths["body"]; found {
		t.Errorf("ObservedPaths still lists body: %v", got.ObservedPaths)
	}
}
