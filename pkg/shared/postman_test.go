package shared

import (
	"os"
	"testing"

	"artemis/pkg/shared/migrate"
)

// knownTypes stands in for what the registry hands the loader in a real run.
// This package cannot read the registry -- the executors import it -- so the
// list is written out here.
var knownTypes = []string{"api", "exec"}

// chdir moves to dir for the length of the test. testing.T.Chdir needs a newer
// go directive than this module declares.
func chdir(t *testing.T, dir string) {
	t.Helper()
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(was); err != nil {
			t.Fatal(err)
		}
	})
}

// A generated file has to pass the same validation a hand-written one does:
// strict decoding and a known step type on every step.
func TestConvertJsonToYamlProducesAParseableScenario(t *testing.T) {
	// ConvertJsonToYaml writes the slugified name into the working directory
	// (its filePath argument is unused), so the test has to move there.
	chdir(t, t.TempDir())

	collection := PostmanCollection{
		Info:      Info{Name: "My Collection"},
		Variables: []PostmanVariable{{Key: "url", Value: "https://api.example.com"}},
		Items: []Item{{
			Name: "login",
			Request: PMRequest{
				Method: "POST",
				Url:    RawField{Raw: "https://api.example.com/token"},
				Body:   RawField{Raw: `{"user":"u"}`},
			},
		}},
	}

	if err := ConvertJsonToYaml(collection, ""); err != nil {
		t.Fatalf("ConvertJsonToYaml() = %v, want nil", err)
	}

	config, err := migrate.ParseYAMLFile("my-collection.yaml", knownTypes)
	if err != nil {
		t.Fatalf("migrate.ParseYAMLFile() on the generated file = %v, want nil", err)
	}
	if len(config.Steps) != 1 || config.Steps[0].Type != "api" {
		t.Errorf("generated steps = %+v, want one api step", config.Steps)
	}
}
