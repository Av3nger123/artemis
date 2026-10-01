package shared

import (
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/oliveagle/jsonpath"
	"gopkg.in/yaml.v3"
)

// ParseYAMLFile reads a scenario and returns it only if artemis understands
// every part of it.
//
// Decoding is strict -- KnownFields(true) -- so a typo'd key is an error naming
// the field and its line instead of a key that is quietly dropped, leaving a
// scenario that asserts nothing and "passes". The parsed config is then
// validated, so a step type nothing can execute stops the run before any
// request is sent. Both entry points, `artemis test` and `artemis parse`, come
// through here, which is what makes parse a real validator.
func ParseYAMLFile(filePath string) (models.Config, error) {
	var config models.Config

	yamlFile, err := os.Open(filePath)
	if err != nil {
		return config, err
	}
	defer yamlFile.Close()

	decoder := yaml.NewDecoder(yamlFile)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}

	if err := config.Validate(); err != nil {
		return config, err
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

// ExtractValue reads the value at a capture's path out of a parsed response
// body.
//
// An empty path is rejected before the lookup, and a panic from the jsonpath
// library is turned into an error: a scenario that omits `path:` under scripts:,
// or writes one the library chokes on, must fail its step with a reason rather
// than taking the whole run down with a stack trace.
func ExtractValue(data map[string]interface{}, binding models.Script) (val interface{}, err error) {
	if strings.TrimSpace(binding.Path) == "" {
		return nil, errors.New("no path given")
	}
	defer func() {
		if r := recover(); r != nil {
			val, err = nil, fmt.Errorf("path %q could not be read: %v", binding.Path, r)
		}
	}()

	val, err = jsonpath.JsonPathLookup(data, binding.Path)
	if err != nil {
		return nil, fmt.Errorf("path %q not found in the response: %w", binding.Path, err)
	}
	// A captured value goes to the log, never to stdout: it is often a token,
	// and the terminal belongs to the step list.
	logger.Logger.Debug("Captured value", "key", binding.Key, "path", binding.Path)
	return val, nil
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
			Scripts: []models.Script{},
			Name:    val.Name,
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
