package migrate

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/expand"
	"artemis/pkg/dsl/front"
)

func migrated(t *testing.T, in map[string]string) map[string]string {
	t.Helper()
	out, _, err := Files(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantIn(t *testing.T, file, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s has no %q:\n%s", file, w, got)
		}
	}
}

func TestMigrateStepNames(t *testing.T) {
	out := migrated(t, map[string]string{
		"flows/f.art": "import \"../collections/auth.art\"\n\nflow f {\n  step \"Get a token\" {\n    get \"x\"\n  }\n\n  step \"log in\" = auth.login {}\n}\n",
	})
	wantIn(t, "flows/f.art", out["flows/f.art"],
		"step get_a_token as \"Get a token\" {", "step log_in as \"log in\" = auth.login {}")
}

func TestMigrateCollectionTitle(t *testing.T) {
	out := migrated(t, map[string]string{
		"collections/orders.art": "collection \"Orders API\" {\n  request create {\n    get \"x\"\n  }\n}\n",
	})
	wantIn(t, "collections/orders.art", out["collections/orders.art"], "collection orders as \"Orders API\" {")
}

func TestMigrateNameClash(t *testing.T) {
	in := map[string]string{
		"flows/f.art":     "flow f {\n  step \"a b\" {\n    get \"x\"\n  }\n\n  step \"a-b\" {\n    get \"y\"\n  }\n}\n",
		"scenarios/s.art": "flow \"../flows/f.art\"\n\nscenario \"a b\" {}\n\nscenario \"a-b\" {}\n",
	}
	out := migrated(t, in)
	wantIn(t, "flows/f.art", out["flows/f.art"], "step a_b as \"a b\" {", "step a_b_2 as \"a-b\" {")
	wantIn(t, "scenarios/s.art", out["scenarios/s.art"], "scenario a_b as \"a b\" {}", "scenario a_b_2 as \"a-b\" {}")
	u := front.CompileWith("scenarios/s.art", out["scenarios/s.art"], expand.MapLoader(out))
	if u.Bag.Len() != 0 {
		t.Errorf("the migrated files do not compile: %v", u.Bag.All())
	}
}

func TestMigrateIdempotent(t *testing.T) {
	once := migrated(t, map[string]string{
		"flows/f.art":            "flow f {\n  step \"Get a token\" {\n    get \"x\"\n  }\n}\n",
		"collections/orders.art": "collection \"Orders API\" {\n  request create {\n    get \"x\"\n  }\n}\n",
	})
	twice := migrated(t, once)
	for k, v := range once {
		if twice[k] != v {
			t.Errorf("%s changed on a second migration:\n%s\n--- was ---\n%s", k, twice[k], v)
		}
	}
}

// A quoted name that is already an id needs no label: it reads the same.
func TestMigrateNoRedundantLabel(t *testing.T) {
	out := migrated(t, map[string]string{
		"flows/f.art":            "flow f {\n  step \"pay\" {\n    get \"x\"\n  }\n}\n",
		"collections/orders.art": "collection \"orders\" {\n  request create {\n    get \"x\"\n  }\n}\n",
	})
	wantIn(t, "flows/f.art", out["flows/f.art"], "step pay {")
	wantIn(t, "collections/orders.art", out["collections/orders.art"], "collection orders {")
}
