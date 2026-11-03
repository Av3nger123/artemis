package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The flow's comment is above the scenario, once, and the flow's own steps
// get none; a step an item call brought in gets its own at any depth, setup
// and blocks included.
func TestExpandPlacesEachOriginComment(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"collections/auth.art": "collection \"auth\" {\n  request login {\n    var url\n    post \"${url}/token\"\n    var token = body.t\n  }\n}\n",
		"flows/f.art": "import \"../collections/auth.art\"\n\nflow f {\n  var url = \"http://h\"\n  setup {\n    step \"log in\" = auth.login { }\n  }\n" +
			"  step \"a\" {\n    get \"${url}/a\"\n  }\n  if true {\n    step \"again\" = auth.login { }\n  }\n  step \"b\" {\n    get \"${url}/b\"\n  }\n}\n",
		"scenarios/x.art": "flow \"../flows/f.art\"\n\nscenario \"one\" { }\n",
	})
	out, stderr, err := runCLI(t, "expand", filepath.Join(dir, "scenarios", "x.art"))
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	flows := filepath.Join(dir, "flows", "f.art")
	coll := filepath.Join(dir, "collections", "auth.art")
	order := []string{
		"# from f (" + flows + ":3) via " + filepath.Join(dir, "scenarios", "x.art") + ":3\nscenario \"one\" {",
		"setup {\n    # from auth.login (" + coll + ":2) via " + flows + ":6\n    step \"log in\" {",
		"  step \"a\" {",
		"    # from auth.login (" + coll + ":2) via " + flows + ":12\n    step \"again\" {",
		"  step \"b\" {",
	}
	rest := out
	for _, want := range order {
		i := strings.Index(rest, want)
		if i < 0 {
			t.Fatalf("missing, or out of order: %q\n%s", want, out)
		}
		rest = rest[i+len(want):]
	}
	if n := strings.Count(out, "# from f "); n != 1 {
		t.Errorf("the flow's comment is there %d times, want once:\n%s", n, out)
	}
}
