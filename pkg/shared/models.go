package shared

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

type Collection struct {
	Items []struct {
		Name    string `json:"name"`
		Request struct {
			Method string `json:"method"`
			Body   struct {
				Raw string `json:"raw"`
			} `json:"body"`
			Url struct {
				Raw string `json:"raw"`
			} `json:"url"`
		}
	} `json:"item"`
	Variables []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"variable"`
}
