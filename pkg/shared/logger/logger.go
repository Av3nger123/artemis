package logger

import (
	"fmt"
	"log/slog"
	"os"
)

var Logger *slog.Logger

func InitLog(filePath string) *os.File {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Println("Error opening log file:", err)
		os.Exit(1)
	}

	Logger = slog.New(slog.NewJSONHandler(file, nil))
	return file
}
