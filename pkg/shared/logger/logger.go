// Package logger holds the JSON log artemis writes when asked for one.
//
// The log file is opt-in: without --log there is no file, and Logger discards
// every record rather than being nil, so no call site has to check whether
// logging was set up.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Logger is where every record goes. It discards them until InitLog points it
// at a file.
var Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))

// nopCloser is what InitLog hands back when there is no file to close.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// InitLog points Logger at filePath and returns the thing to close when the run
// is over. An empty path leaves Logger discarding and closes nothing: that is
// what an omitted --log means.
func InitLog(filePath string) (io.Closer, error) {
	if filePath == "" {
		return nopCloser{}, nil
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nopCloser{}, fmt.Errorf("opening log file %s: %w", filePath, err)
	}

	Logger = slog.New(slog.NewJSONHandler(file, nil))
	return file, nil
}
