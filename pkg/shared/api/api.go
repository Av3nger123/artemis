package api

import (
	"artemis/pkg/result"
	"artemis/pkg/shared"
	"artemis/pkg/shared/assert"
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func CallAPI(step models.Step, config *map[string]interface{}) (*http.Response, error) {
	url, err := shared.TransformText(step.Request.URL, *config)
	if err != nil {
		return nil, fmt.Errorf("rendering request url: %w", err)
	}
	body, err := shared.TransformText(step.Request.Body, *config)
	if err != nil {
		return nil, fmt.Errorf("rendering request body: %w", err)
	}

	req, err := http.NewRequest(step.Request.Method, url, bytes.NewBuffer([]byte(body)))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	for key, value := range step.Request.Headers {
		val, err := shared.TransformText(value, *config)
		if err != nil {
			return nil, fmt.Errorf("rendering header %q: %w", key, err)
		}
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
		logger.Logger.Warn("Error reading response body", "name", api.Name, "error", err.Error())
	}
	var response map[string]interface{}

	if resp.StatusCode == api.Response.StatusCode {
		if err := json.Unmarshal(responseBody, &response); err != nil {
			logger.Logger.Warn("Error parsing response body", "name", api.Name, "error", err.Error())
		}
	} else {
		// The body of an unexpected status is log material, not terminal
		// output: it would land unterminated in the middle of the step list.
		logger.Logger.Warn("Unexpected response status", "name", api.Name, "status", resp.StatusCode, "body", string(responseBody))
	}

	return response
}

// AssertResponse checks every body check of the step against the response.
// The engine itself is pkg/shared/assert; this is the runner's door into it.
func AssertResponse(step models.Step, response map[string]interface{}) []result.AssertionResult {
	return assert.Body(step, response)
}

// AllPassed reports whether every assertion in as passed.
func AllPassed(as []result.AssertionResult) bool {
	for _, a := range as {
		if !a.Passed() {
			return false
		}
	}
	return true
}

func ExecuteScripts(data map[string]interface{}, api models.Step, config *map[string]interface{}) error {
	configMap := *config
	// config population from response
	for i := range api.Scripts {
		val, err := shared.ExtractValue(data, api.Scripts[i])
		if err != nil {
			// The cause is kept: "no path" and "that path is not in this body"
			// are different mistakes, and the old message said neither.
			return fmt.Errorf("capture %q: %w", api.Scripts[i].Key, err)
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
