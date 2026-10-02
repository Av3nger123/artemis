package shared

import (
	"bytes"

	"artemis/pkg/shared/env"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
	"encoding/json"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseYAMLFile reads a scenario and returns it only if artemis understands
// every part of it.
//
// Decoding is strict -- KnownFields(true) -- so a typo'd key is an error naming
// the field and its line instead of a key that is quietly dropped, leaving a
// scenario that asserts nothing and "passes". The parsed config is then
// validated against knownTypes -- the step types that are actually registered,
// which the caller reads off executor.Registry.Types -- so a step type nothing
// can execute stops the run before any request is sent. Both entry points,
// `artemis test` and `artemis parse`, come through here, which is what makes
// parse a real validator.
//
// The file is then read a second time as a plain node tree, to stamp onto the
// config the line each part of it was written on (ART-12). The bytes are read
// once and decoded twice rather than decoded once into a node and converted,
// because KnownFields is a property of the decoder and does not survive being
// handed a node: strictness is worth more than a pass over the file.
func ParseYAMLFile(filePath string, knownTypes []string) (models.Config, error) {
	var config models.Config

	raw, err := os.ReadFile(filePath)
	if err != nil {
		return config, err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}

	if err := config.Validate(knownTypes); err != nil {
		return config, err
	}

	// Past the point of no return: the scenario is good. A second decode of
	// bytes the first one accepted cannot fail, and if it somehow did, the
	// scenario still runs -- with no line numbers, which is what a scenario
	// built in Go has always had.
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err == nil {
		annotateLines(&config, &doc)
	}

	return config, nil
}

func ParsePostmanJSON(filePath string) (models.PostmanCollection, error) {
	var collection models.PostmanCollection
	jsonFile, err := os.Open(filePath)
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

func ConvertJsonToYaml(collection models.PostmanCollection, filePath string) error {
	apiConfig := models.Config{
		Steps:     make([]models.Step, 0),
		Name:      collection.Info.Name,
		Variables: make([]models.Variable, 0),
		Type:      "functional",
	}
	for _, val := range collection.Items {
		apiConfig.Steps = append(apiConfig.Steps, models.Step{
			// Set explicitly: a generated file has to pass the same
			// validation a hand-written one does.
			Type: "api",
			Request: models.Request{
				URL:    val.Request.Url.Raw,
				Method: val.Request.Method,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: val.Request.Body.Raw,
			},
			Name: val.Name,
			Response: models.Response{
				StatusCode: 200,
				Body:       []models.BodyCheck{},
			},
		})
	}
	for _, val := range collection.Variables {
		apiConfig.Variables = append(apiConfig.Variables, models.Variable{Name: val.Key, Value: val.Value})
	}

	file, err := os.Create(utils.Slugify(collection.Info.Name) + ".yaml")
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

// SubstituteEnvVars replaces every {{env.NAME}} in input with NAME's value from
// the process environment. A name that is not set becomes the empty string: an
// absent variable is how a scenario says "no token", and failing the run over it
// would make every optional variable mandatory.
//
// The scan is forward-only and single-pass, like TransformText's (ART-6): it
// never looks backwards for a closing delimiter, so "}}{{env.X}}" and
// "{{other}} {{env.X}}" render instead of panicking on a reversed slice, and a
// value that itself contains {{env.X}} is never re-expanded, so a
// self-referencing variable cannot loop forever.
//
// A reference that is never closed is copied through as it stands -- the
// placeholders artemis itself resolves are TransformText's job, and it is the one
// that reports an unclosed one.
func SubstituteEnvVars(input string) interface{} {
	const prefix = "{{env."

	var out strings.Builder
	rest := input
	for {
		start := strings.Index(rest, prefix)
		if start < 0 {
			out.WriteString(rest)
			return out.String()
		}
		body := rest[start+len(prefix):]
		end := strings.Index(body, "}}")
		if end < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:start])
		out.WriteString(env.GetEnvValue(body[:end]))
		rest = body[end+len("}}"):]
	}
}
