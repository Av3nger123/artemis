package lower

import (
	"slices"
	"testing"

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
	if first.Secrets.Any() {
		t.Errorf("the capturing step marks nothing of its own: %+v", first.Secrets)
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
