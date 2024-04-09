package shared

import (
	"artemis/pkg/shared/models"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func CallAPI(api models.API, config *map[string]interface{}) (*http.Response, error) {
	url, _ := renderTemplate(api.Url, *config)
	body, _ := renderTemplate(api.Body, *config)

	req, err := http.NewRequest(api.Method, url, bytes.NewBuffer([]byte(body)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	for key, value := range api.Headers {
		val, _ := renderTemplate(value, *config)
		req.Header.Set(key, val)
	}
	Logger.Info("API call", "name", api.Name, "url", url, "method", api.Method, "headers", req.Header, "body", body)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error performing request: %v", err)
	}
	return resp, nil
}

func ParseResponse(api models.API, resp *http.Response) map[string]interface{} {
	if resp == nil {
		return nil
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("error reading response body: %v", err)
	}
	var response map[string]interface{}

	if resp.StatusCode == int(api.Test.Status) {
		if err := json.Unmarshal(responseBody, &response); err != nil {
			fmt.Printf("error parsing response body: %v", err)
		}
	} else {
		fmt.Printf("Error occurred %s", string(responseBody))
	}

	return response
}

func AssertResponse(api models.API, response map[string]interface{}) bool {
	assert := true
	for _, val := range api.Test.ResponseBody {
		assert = assert && executeCondition(val.Path, val.Value, val.Type, response)
	}
	return assert
}

func executeCondition(path string, value string, valueType string, response map[string]interface{}) bool {
	return false
}

func PostAPICall(data map[string]interface{}, api models.API, config *map[string]interface{}) error {
	configMap := *config
	// config population from response
	for i := range api.Bindings {
		val, err := ExtractValue(data, api.Bindings[i])
		if err != nil {
			return fmt.Errorf("path %s not found in the response ", api.Bindings[i])

		}
		configMap[api.Bindings[i].Key] = val
	}

	// config population from input
	// if api.Input != nil || len(api.Input) > 0 {
	// 	val, _ := json.Marshal(data)
	// 	fmt.Println(string(val))
	// 	for _, key := range api.Input {
	// 		var input string
	// 		fmt.Println("================================")
	// 		fmt.Printf("Enter value for key %s:\n", key.Key)
	// 		fmt.Scanln(&input)
	// 		configMap[key.Key] = input
	// 		Logger.Info("User input", "key", key.Key, "value", input)
	// 	}
	// }
	*config = configMap
	return nil
}

func LogDecorator(f func(models.API, *map[string]interface{}) (*http.Response, error)) func(models.API, *map[string]interface{}) (*http.Response, error) {
	return func(api models.API, config *map[string]interface{}) (*http.Response, error) {
		startTime := time.Now()
		resp, err := f(api, config)
		if err != nil {
			return nil, err
		}
		Logger.Info("result:", "name", api.Name, "time", time.Since(startTime), "status", resp.StatusCode)
		return resp, err
	}
}
