package api

import (
	"artemis/pkg/result"
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

// AssertResponse checks every body check of the step against the response and
// returns one result per check, carrying the path, what was expected and what
// was found. A path that does not resolve is an errored assertion, not a
// silently failed one.
func AssertResponse(step models.Step, response map[string]interface{}) []result.AssertionResult {
	assertions := make([]result.AssertionResult, 0, len(step.Response.Body))
	for _, check := range step.Response.Body {
		a := result.Assertion{
			Step:     step.Name,
			Kind:     "body",
			Path:     check.Path,
			Operator: "equals",
			Expected: check.Value,
		}

		extractedValue, err := jsonpath.JsonPathLookup(response, check.Path)
		if err != nil {
			assertions = append(assertions, a.Errored(err))
			continue
		}

		if slice, ok := extractedValue.([]interface{}); ok {
			if len(slice) == 0 {
				assertions = append(assertions, a.Errored(fmt.Errorf("path %s resolved to an empty list", check.Path)))
				continue
			}
			extractedValue = slice[0]
		}

		a.Actual = extractedValue
		if extractedValue == check.Value {
			assertions = append(assertions, a.Pass())
		} else {
			assertions = append(assertions, a.Fail())
		}
	}
	return assertions
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
