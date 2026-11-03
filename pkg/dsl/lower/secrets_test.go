package lower

import (
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

// Nothing marks a binding secret: the language has no secret now (the spec's
// Open item, credentials in reports), and a secret var the parser still reads
// for artemis migrate -- pkg/dsl/front rejects it -- marks nothing either. The
// withholding stays, unmarked, for the design that will mark one.
func TestNothingIsMarkedSecret(t *testing.T) {
	t.Setenv("P", "hunter2")
	sc, scope := bound(t, `scenario "s" {
  secret var pw = env("P")
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
		t.Errorf("Secrets.Any() = true: %+v", m.Secrets)
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
