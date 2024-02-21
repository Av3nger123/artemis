package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

func ExecuteAPI(api API, config *map[string]interface{}) {
	if api.Meta.Type == "multiple" {
		executeMultipleAPI(api, config)
	} else {
		executeSingleAPI(api, config)
	}
}

func executeMultipleAPI(api API, config *map[string]interface{}) {
	i := 0
	var resp *http.Response
	var err error
	for i < api.Meta.Max || (resp != nil && resp.StatusCode != 200) {
		i++
		resp, err = executeAndDecorateAPI(api, config)
		time.Sleep(time.Second * time.Duration(api.Meta.Interval))

		// exit condition
		response := analyzeResponse(resp)
		val, _ := ExtractValue(response, api.Meta.Exit.Key)
		if convVal(val, api.Meta.Exit.Type) == api.Meta.Exit.Value {
			if err := postScript(response, api, config); err != nil {
				fmt.Printf("%v: %s\n", err, response)
			}
			val, _ := json.Marshal(response)
			fmt.Println(string(val))
			for _, key := range api.Input {
				var input string
				fmt.Printf("Enter %s:", key.Key)
				fmt.Scanln(&input)
				(*config)[key.Key] = input
			}
			return
		}
	}
	if err != nil {
		slog.Warn("Error while executing API", "name", api.Name, "error", err.Error())
		return
	}
}

func executeSingleAPI(api API, config *map[string]interface{}) {
	resp, err := executeAndDecorateAPI(api, config)
	if err != nil {
		slog.Warn("Error while executing API", "name", api.Name, "error", err.Error())
		return
	}
	response := analyzeResponse(resp)
	if response != nil {
		if err := postScript(response, api, config); err != nil {
			fmt.Printf("%v: %s\n", err, response)
		}
	}
}

func executeAndDecorateAPI(api API, config *map[string]interface{}) (*http.Response, error) {
	return apiDecorator(callAPI)(api, config)
}

func analyzeResponse(resp *http.Response) map[string]interface{} {
	if resp == nil {
		return nil
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("error reading response body: %v", err)
	}
	var response map[string]interface{}

	if resp.StatusCode == 200 {
		if err := json.Unmarshal(responseBody, &response); err != nil {
			fmt.Printf("error parsing response body: %v", err)
		}
	} else {
		fmt.Printf("Error occurred %s", string(responseBody))
	}
	return response
}

func postScript(data map[string]interface{}, api API, config *map[string]interface{}) error {
	configMap := *config
	for i := range api.Variables {
		val, err := ExtractValue(data, api.Variables[i].Value)
		if err != nil {
			return fmt.Errorf("path %s not found in the response ", api.Variables[i].Value)

		}
		configMap[api.Variables[i].Key] = val
	}
	*config = configMap
	return nil
}

func callAPI(api API, config *map[string]interface{}) (*http.Response, error) {

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

func apiDecorator(f func(API, *map[string]interface{}) (*http.Response, error)) func(API, *map[string]interface{}) (*http.Response, error) {
	return func(api API, config *map[string]interface{}) (*http.Response, error) {
		startTime := time.Now()
		resp, err := f(api, config)
		duration := time.Since(startTime)
		if err != nil {
			return nil, err
		}
		slog.Info("result:", "name", api.Name, "time", duration, "status", resp.StatusCode)
		return resp, err
	}
}
