package shared

import (
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
	"encoding/json"
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
func ExtractValue(data map[string]interface{}, binding models.Script) (interface{}, error) {
	val, err := jsonpath.JsonPathLookup(data, binding.Path)
	if err != nil {
		return nil, err
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

func SubstituteEnvVars(input string) interface{} {
	envVarPrefix := "{{env."
	for strings.Contains(input, envVarPrefix) {
		startIndex := strings.Index(input, envVarPrefix)
		endIndex := strings.Index(input, "}}")
		if endIndex == -1 {
			break
		}
		// Extract the environment variable name
		varName := input[startIndex+len(envVarPrefix) : endIndex]

		// Substitute the environment variable value
		varValue := env.GetEnvValue(varName)
		input = strings.Replace(input, input[startIndex:endIndex+len("}}")], varValue, 1)
	}

	return input
}
