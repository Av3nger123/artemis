package env

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

func InitEnv(filePath string) {
	err := godotenv.Load(filePath)
	if err != nil {
		fmt.Println("failed to load env from", filePath)
	}
}

func GetEnvValue(key string) string {
	return os.Getenv(key)
}
