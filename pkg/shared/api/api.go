package api

import (
	"artemis/pkg/shared"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/oliveagle/jsonpath"
)

func CallAPI(step models.Step, config *map[string]interface{}) (*http.Response, error) {
	url, _ := shared.TransformText(step.Request.URL, *config)
	body, _ := shared.TransformText(step.Request.Body, *config)

	req, err := http.NewRequest(step.Request.Method, url, bytes.NewBuffer([]byte(body)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	for key, value := range step.Request.Headers {
		val, _ := shared.TransformText(value, *config)
		req.Header.Set(key, val)
	}
	logger.Logger.Info("API call", "name", step.Name, "url", url, "method", step.Request.Method, "headers", req.Header, "body", body)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error performing request: %v", err)
	}
	return resp, nil
}

func ParseResponse(api models.Step, resp *http.Response) map[string]interface{} {
	if resp == nil {
		return nil
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("error reading response body: %v", err)
	}
	var response map[string]interface{}

	if resp.StatusCode == int(api.Response.StatusCode) {
		if err := json.Unmarshal(responseBody, &response); err != nil {
			fmt.Printf("error parsing response body: %v", err)
		}
	} else {
		fmt.Printf("Error occurred %s", string(responseBody))
	}

	return response
}

func AssertResponse(api models.Step, response map[string]interface{}) bool {
	assert := true
	for _, val := range api.Response.Body {
		var tempAssert bool
		extractedValue, _ := jsonpath.JsonPathLookup(response, val.Path)
		if slice, ok := extractedValue.([]interface{}); ok {
			tempAssert = slice[0] == val.Value
		} else {
			tempAssert = extractedValue == val.Value
		}
		assert = assert && tempAssert
	}
	return assert
}

func ExecuteScripts(data map[string]interface{}, api models.Step, config *map[string]interface{}) error {
	configMap := *config
	// config population from response
	for i := range api.Scripts {
		val, err := shared.ExtractValue(data, api.Scripts[i])
		if err != nil {
			return fmt.Errorf("path %s not found in the response ", api.Scripts[i].Path)

		}
		configMap[api.Scripts[i].Key] = val
	}

	// config population from input
	// for _, v  :=api.Bindings != nil || len(api.Input) > 0 {
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
