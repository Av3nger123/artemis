package cli

import (
	"artemis/pkg/shared"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/exp/slog"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test APIs defined in YAML file",
	Long:  "Test APIs defined in YAML file and display the responses",
	Run: func(cmd *cobra.Command, args []string) {
		testAPIs(cmd)
	},
}

func testAPIs(cmd *cobra.Command) {
	config := parseYAMLFile(cmd)
	for i := range config.Apis {
		startTime := time.Now()
		resp, err := shared.CallAPI(config.Apis[i], &config.Configuration)
		duration := time.Since(startTime)
		if err != nil {
			slog.Warn("Error while executing API", "name", config.Apis[i].Name, "error", err.Error())
			continue
		}
		slog.Info("result:", "name", config.Apis[i].Name, "time", duration, "status", resp.StatusCode)
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("error reading response body: %v", err)
		}
		if resp.StatusCode == 200 {
			var response map[string]interface{}
			if err := json.Unmarshal(responseBody, &response); err != nil {
				fmt.Printf("error parsing response body: %v", err)
			}
			if err := postScript(response, config.Apis[i], &config.Configuration); err != nil {
				fmt.Printf("%v: %s\n", err, string(responseBody))
			}
		} else {
			fmt.Printf("Error occurred %s", string(responseBody))
		}

	}
}

func postScript(data map[string]interface{}, api shared.API, config *map[string]interface{}) error {
	configMap := *config
	for i := range api.Variables {
		val, err := shared.ExtractValue(data, api.Variables[i].Path)
		if err != nil {
			return fmt.Errorf("path %s not found in the response ", api.Variables[i].Path)

		}
		configMap[api.Variables[i].Name] = val
	}
	*config = configMap
	return nil
}
