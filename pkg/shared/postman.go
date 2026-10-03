// Package shared is what is left of the YAML-era front end: the Postman
// collection reader, and the documentation tests that hold README.md and
// SPEC.md to the code.
//
// Everything else moved out in ART-40. The scenario loader and the YAML model
// are in pkg/shared/migrate, with `artemis migrate` as their only caller; the
// `{{}}` substituter is gone, because interpolation is a lexer concern in the
// DSL and pkg/eval owns the rules its renderValue had.
package shared

import (
	"encoding/json"
	"os"

	"artemis/pkg/shared/migrate"
	"artemis/pkg/shared/utils"

	"gopkg.in/yaml.v3"
)

type RawField struct {
	Raw string `json:"raw"`
}
type PMRequest struct {
	Method string   `json:"method"`
	Body   RawField `json:"body"`
	Url    RawField `json:"url"`
}
type Item struct {
	Name    string    `json:"name"`
	Request PMRequest `json:"request"`
}
type PostmanVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type Info struct {
	Name string `json:"name"`
}
type PostmanCollection struct {
	Items     []Item            `json:"item"`
	Variables []PostmanVariable `json:"variable"`
	Info      Info              `json:"info"`
}

// ParsePostmanJSON reads a Postman collection.
func ParsePostmanJSON(filePath string) (PostmanCollection, error) {
	var collection PostmanCollection
	jsonFile, err := os.Open(filePath) //nolint:gosec // the path is the one the user named
	if err != nil {
		return collection, err
	}
	defer jsonFile.Close()
	decoder := json.NewDecoder(jsonFile)
	if err := decoder.Decode(&collection); err != nil {
		return collection, err
	}
	return collection, nil
}

// ConvertJsonToYaml writes collection out as a YAML scenario, named after the
// collection and dropped in the working directory.
//
// It builds a migrate.Config rather than a models.Step, because the thing it
// writes is a file and models is the runtime step now (ART-40). The output is
// therefore exactly what `artemis migrate` can read, which is what makes
// `generate` then `migrate` a working two-step path to a `.art` file until
// ART-41 collapses it into one.
func ConvertJsonToYaml(collection PostmanCollection, filePath string) error {
	apiConfig := migrate.Config{
		Steps:     make([]migrate.Step, 0),
		Name:      collection.Info.Name,
		Variables: make([]migrate.Variable, 0),
		Type:      "functional",
	}
	for _, val := range collection.Items {
		apiConfig.Steps = append(apiConfig.Steps, migrate.Step{
			// Set explicitly: a generated file has to pass the same
			// validation a hand-written one does.
			Type: "api",
			Request: migrate.Request{
				URL:    val.Request.Url.Raw,
				Method: val.Request.Method,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: val.Request.Body.Raw,
			},
			Name: val.Name,
			Response: migrate.Response{
				StatusCode: 200,
				Body:       []migrate.BodyCheck{},
			},
		})
	}
	for _, val := range collection.Variables {
		apiConfig.Variables = append(apiConfig.Variables, migrate.Variable{Name: val.Key, Value: val.Value})
	}

	file, err := os.Create(utils.Slugify(collection.Info.Name) + ".yaml") //nolint:gosec // the name is the collection's own
	if err != nil {
		return err
	}
	defer file.Close()

	// Two spaces, not yaml.v3's default four: the version bump should not
	// silently reshape every file this generator has ever written.
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	if err := encoder.Encode(&apiConfig); err != nil {
		return err
	}
	return encoder.Close()
}
