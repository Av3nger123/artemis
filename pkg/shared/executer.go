package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func CallAPI(api API, config map[string]interface{}) (map[string]interface{}, error) {
	req, err := http.NewRequest(api.Method, api.Url, bytes.NewBuffer([]byte(api.Body)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	for key, value := range api.Headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error performing request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error parsing response body: %v", err)
	}
	return response, nil
}
