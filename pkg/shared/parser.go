package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v2"
)

func ParseYAMLFile(filePath string) (APIConfig, error) {
	var config APIConfig

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

func ParsePostmanJSON(filePath string) (Collection, error) {
	var collection Collection
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

func ConvertJsonToYaml(collection Collection, filePath string) error {
	fmt.Println(collection)
	apiConfig := APIConfig{
		Apis:          make([]API, 0),
		Configuration: map[string]interface{}{},
	}
	for _, val := range collection.Items {
		apiConfig.Apis = append(apiConfig.Apis, API{
			Name:   val.Name,
			Url:    val.Request.Url.Raw,
			Method: val.Request.Method,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Meta: MetaData{
				"single", 0, 0, Variable{},
			},
			Body:      val.Request.Body.Raw,
			Variables: []Variable{},
		})
	}
	for _, val := range collection.Variables {
		apiConfig.Configuration[val.Key] = val.Value
	}

	data, err := yaml.Marshal(&apiConfig)
	if err != nil {
		return err
	}

	file, err := os.Create(strings.TrimSuffix(filePath, ".json") + ".yaml")
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return err
	}
	return nil
}

func convVal(val any, valType string) string {
	if valType == "boolean" {
		return strconv.FormatBool(val.(bool))
	} else if valType == "number" {
		return strconv.FormatInt(val.(int64), 10)
	}
	return ""
}
