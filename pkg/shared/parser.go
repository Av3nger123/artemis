package shared

import (
	"artemis/pkg/shared/env"
	"artemis/pkg/shared/models"
	"artemis/pkg/shared/utils"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/oliveagle/jsonpath"
	"gopkg.in/yaml.v2"
)

func ParseYAMLFile(filePath string) (models.Config, error) {
	var config models.Config

	yamlFile, err := os.Open(filePath)
	if err != nil {
		return config, err
	}
	defer yamlFile.Close()

	decoder := yaml.NewDecoder(yamlFile)
	if err := decoder.Decode(&config); err != nil {
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
	fmt.Println(val)
	if err != nil {
		return nil, err
	}
	return val, nil
}

func TransformText(templateStr string, config map[string]interface{}) (string, error) {
	final := ""
	i := 0
	for ; i < len(templateStr); i++ {
		if templateStr[i] == '{' && templateStr[i+1] == '{' {
			start := i + 2
			for templateStr[i] != '}' {
				i++
			}
			val := config[templateStr[start:i]]
			if val != nil {
				final += val.(string)
			} else {
				final += templateStr[start-2 : i+1]
			}
			i++
		} else {
			final += templateStr[i : i+1]
		}
	}
	return final, nil
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
			Retry:   1,
			Response: models.Response{
				StatusCode: 200,
				Body:       []models.BodyCheck{},
			},
		})
	}
	for _, val := range collection.Variables {
		apiConfig.Variables = append(apiConfig.Variables, models.Variable{Name: val.Key, Value: val.Value})
	}

	data, err := yaml.Marshal(&apiConfig)
	if err != nil {
		return err
	}

	file, err := os.Create(utils.Slugify(collection.Info.Name) + ".yaml")
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return err
	}
	return nil
}

func TypeCast(val any, valType string) string {
	if val == nil {
		return ""
	}
	if valType == "boolean" {
		return strconv.FormatBool(val.(bool))
	} else if valType == "number" {
		return strconv.FormatInt(val.(int64), 10)
	}
	return ""
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
