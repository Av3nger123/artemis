package env

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// InitEnv loads filePath into the process environment. It returns the failure
// rather than printing it: whether a file that did not load is worth a word on
// the terminal depends on whether the user asked for that file, and only the
// command knows that.
func InitEnv(filePath string) error {
	if err := godotenv.Load(filePath); err != nil {
		return fmt.Errorf("loading env from %s: %w", filePath, err)
	}
	return nil
}

func GetEnvValue(key string) string {
	return os.Getenv(key)
}
