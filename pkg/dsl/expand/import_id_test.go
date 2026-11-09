package expand

import (
	"strings"
	"testing"

	"artemis/pkg/dsl/diag"
)

const backendAPIs = "collection backend_apis as \"Backend APIs\" {\n  request ping {\n    get \"x\"\n  }\n}\n"

func TestImportByCollectionID(t *testing.T) {
	e := expandFlows(t, "import \"collections/Backend APIs.art\"\n\nflow f {\n  step p = backend_apis.ping {}\n}\n",
		map[string]string{"collections/Backend APIs.art": backendAPIs})
	if e.diags != "" {
		t.Fatalf("diagnostics:\n%s", e.diags)
	}
}

func TestImportAsStillWins(t *testing.T) {
	e := expandFlows(t, "import \"collections/Backend APIs.art\" as be\n\nflow f {\n  step p = be.ping {}\n}\n",
		map[string]string{"collections/Backend APIs.art": backendAPIs})
	if e.diags != "" {
		t.Fatalf("diagnostics:\n%s", e.diags)
	}
}

func TestImportUnknownByFileNameHintsID(t *testing.T) {
	e := expandFlows(t, "import \"collections/auth_v2.art\"\n\nflow f {\n  step l = auth_v2.login {}\n}\n",
		map[string]string{"collections/auth_v2.art": "collection auth {\n  request login {\n    get \"x\"\n  }\n}\n"})
	got := only(e.bag, diag.UnknownCollection)
	if len(got) != 1 {
		t.Fatalf("%d unknown-collection, want 1:\n%s", len(got), e.diags)
	}
	if !strings.Contains(got[0].Hint, "auth.login") {
		t.Fatalf("hint %q does not name the collection's id", got[0].Hint)
	}
}
