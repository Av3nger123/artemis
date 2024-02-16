package shared

import (
	"bytes"
	"fmt"
	"net/http"
)

func CallAPI(api API, config *map[string]interface{}) (*http.Response, error) {

	method, _ := renderTemplate(api.Method, *config)
	url, _ := renderTemplate(api.Url, *config)
	body, _ := renderTemplate(api.Body, *config)

	req, err := http.NewRequest(method, url, bytes.NewBuffer([]byte(body)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	for key, value := range api.Headers {
		val, _ := renderTemplate(value, *config)
		req.Header.Set(key, val)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error performing request: %v", err)
	}
	return resp, nil
}
