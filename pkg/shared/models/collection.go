package models

type Variable struct {
	Key   string      `yaml:"name"`
	Value interface{} `yaml:"value"`
}

type MetaData struct {
	RetryEnabled   bool `yaml:"retry_enabled"`
	MaxRetries     int  `yaml:"retry_limit"`
	RetryFrequency int  `yaml:"retry_frequency"`
}

type BodyAssert struct {
	Value string `yaml:"value"`
	Path  string `yaml:"path"`
	Type  string `yaml:"type"`
}

type Test struct {
	Status       int32        `yaml:"status_code"`
	ResponseBody []BodyAssert `yaml:"response_body"`
}

type Binding struct {
	Key  string `yaml:"key"`
	Path string `yaml:"path"`
}

type API struct {
	Name     string            `yaml:"name"`
	Url      string            `yaml:"endpoint"`
	Method   string            `yaml:"method"`
	Headers  map[string]string `yaml:"headers"`
	Body     string            `yaml:"request_body"`
	Bindings []Binding         `yaml:"post_scripts"`
	Meta     MetaData          `yaml:"meta"`
	Test     Test              `yaml:"test"`
}

type Collection struct {
	Name        string     `yaml:"collection_name"`
	Type        string     `yaml:"collection_type"`
	Variables   []Variable `yaml:"collection_variables"`
	VariableMap map[string]interface{}
}
type APIConfig struct {
	Collection Collection `yaml:"api_collection"`
	Apis       []API      `yaml:"apis"`
}
