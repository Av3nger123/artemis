package shared

import (
	"artemis/pkg/shared/models"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v2"
)

func ParseYAMLFile(filePath string) (models.APIConfig, error) {
	var config models.APIConfig

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
func ExtractValue(data map[string]interface{}, path string) (interface{}, error) {
	keys := strings.Split(path, ".")
	current := data
	for _, key := range keys {
		val, ok := current[key]
		if !ok {
			return nil, fmt.Errorf("path %s not found in response", path)
		}
		if nested, ok := val.(map[string]interface{}); ok {
			current = nested
		} else {
			return val, nil
		}
	}
	return nil, fmt.Errorf("path %s not found in response", path)

}

func renderTemplate(templateStr string, config map[string]interface{}) (string, error) {
	tmpl, err := template.New("template").Parse(templateStr)
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, config); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func ConvertJsonToYaml(collection models.PostmanCollection, filePath string) error {
	return nil
	// fmt.Println(collection)
	// apiConfig := models.APIConfig{
	// 	Apis:       make([]models.API, 0),
	// 	Collection: map[string]interface{}{},
	// }
	// for _, val := range collection.Items {
	// 	apiConfig.Apis = append(apiConfig.Apis, API{
	// 		Name:   val.Name,
	// 		Url:    val.Request.Url.Raw,
	// 		Method: val.Request.Method,
	// 		Headers: map[string]string{
	// 			"Content-Type": "application/json",
	// 		},
	// 		Meta: MetaData{
	// 			"single", 0, 0, Variable{},
	// 		},
	// 		Body:      val.Request.Body.Raw,
	// 		Variables: []Variable{},
	// 	})
	// }
	// for _, val := range collection.Variables {
	// 	apiConfig.Configuration[val.Key] = val.Value
	// }

	// data, err := yaml.Marshal(&apiConfig)
	// if err != nil {
	// 	return err
	// }

	// file, err := os.Create(strings.TrimSuffix(filePath, ".json") + ".yaml")
	// if err != nil {
	// 	return err
	// }
	// defer file.Close()

	// if _, err := file.Write(data); err != nil {
	// 	return err
	// }
	// return nil
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
		varValue := GetEnvValue(varName)
		input = strings.Replace(input, input[startIndex:endIndex+len("}}")], varValue, 1)
	}

	return input
}
