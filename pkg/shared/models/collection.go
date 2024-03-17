package models

type Variable struct {
	Key   string      `yaml:"variable_name"`
	Value interface{} `yaml:"response_path"`
	Type  string      `yaml:"type"`
}

type MetaData struct {
	RetryEnabled   bool `yaml:"retry_enabled"`
	MaxRetries     int  `yaml:"retry_limit"`
	RetryFrequency int  `yaml:"retry_frequency"`
}

type Test struct {
	Status       int32    `yaml:"status_code"`
	ResponseBody []string `yaml:"response_body"`
}

type API struct {
	Name     string            `yaml:"name"`
	Url      string            `yaml:"endpoint"`
	Method   string            `yaml:"method"`
	Headers  map[string]string `yaml:"headers"`
	Body     string            `yaml:"request_body"`
	Bindings []Variable        `yaml:"value_bindings"`
	Meta     MetaData          `yaml:"meta"`
	Test     Test              `yaml:"test"`
}

type Collection struct {
	Name        string     `yaml:"collection_name"`
	Type        string     `yaml:"collection_type"`
	Mode        string     `yaml:"collection_mode"`
	Variables   []Variable `yaml:"collection_variables"`
	VariableMap map[string]interface{}
}
type APIConfig struct {
	Collection Collection `yaml:"api_collection"`
	Apis       []API      `yaml:"apis"`
}
