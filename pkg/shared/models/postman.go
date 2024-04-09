package models

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

type Info struct {
	Name string `json:"name"`
}
type PostmanCollection struct {
	Items     []Item            `json:"item"`
	Variables []PostmanVariable `json:"variable"`
	Info      Info              `json:"info"`
}
