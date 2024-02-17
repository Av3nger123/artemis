package shared

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"gopkg.in/yaml.v2"
)

type Variable struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	Type string `yaml:"type"`
}

type MetaData struct {
	Type     string `yaml:"type"`
	Max      int    `yaml:"max"`
	Interval int    `yaml:"interval"`
}

type API struct {
	Name      string            `yaml:"name"`
	Url       string            `yaml:"url"`
	Method    string            `yaml:"method"`
	Headers   map[string]string `yaml:"headers"`
	Body      string            `yaml:"body"`
	Variables []Variable        `yaml:"variables"`
	Meta      MetaData          `yaml:"meta"`
}
type APIConfig struct {
	Configuration map[string]interface{} `yaml:"configuration"`
	Apis          []API                  `yaml:"apis"`
}

func ParseYAMLFile(filePath string) (APIConfig, error) {
	var config APIConfig

	yamlFile, err := os.Open(filePath)
	if err != nil {
		return config, err
	}

	decoder := yaml.NewDecoder(yamlFile)
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}

	return config, nil

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
