package shared

import (
	"os"

	"gopkg.in/yaml.v2"
)

type APIConfig struct {
	Configuration map[string]interface{} `yaml:"configuration"`
	Apis          []struct {
		Name    string            `yaml:"name"`
		Url     string            `yaml:"url"`
		Method  string            `yaml:"method"`
		Headers map[string]string `yaml:"headers"`
		Body    interface{}       `yaml:"body"`
	} `yaml:"apis"`
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
