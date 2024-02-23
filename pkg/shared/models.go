package shared

type Variable struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	Type  string `yaml:"type"`
}

type MetaData struct {
	Type     string   `yaml:"type"`
	Max      int      `yaml:"max"`
	Interval int      `yaml:"interval"`
	Exit     Variable `yaml:"exit"`
}

type Assert struct {
	Status int32 `yaml:"status"`
}

type API struct {
	Name      string            `yaml:"name"`
	Url       string            `yaml:"url"`
	Method    string            `yaml:"method"`
	Headers   map[string]string `yaml:"headers"`
	Body      string            `yaml:"body"`
	Variables []Variable        `yaml:"variables"`
	Input     []struct {
		Key string `yaml:"key"`
	} `yaml:"input"`
	Meta   MetaData `yaml:"meta"`
	Assert Assert   `yaml:"assert"`
}
type APIConfig struct {
	Configuration map[string]interface{} `yaml:"configuration"`
	Apis          []API                  `yaml:"apis"`
}

type RawField struct {
	Raw string `json:"raw"`
}
type Request struct {
	Method string   `json:"method"`
	Body   RawField `json:"body"`
	Url    RawField `json:"url"`
}
type Item struct {
	Name    string  `json:"name"`
	Request Request `json:"request"`
}
type PostmanVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}
type Collection struct {
	Items     []Item            `json:"item"`
	Variables []PostmanVariable `json:"variable"`
}
