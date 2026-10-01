package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnv writes an env file into a temp dir and returns its path.
func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// godotenv does not overwrite a variable that is already set: the real
// environment wins over the file, which is what lets CI override a committed
// .env without editing it.
func TestInitEnvDoesNotOverwriteWhatIsAlreadySet(t *testing.T) {
	const key = "ARTEMIS_TEST_TOKEN"
	t.Setenv(key, "from-environment")

	if err := InitEnv(writeEnv(t, key+"=from-file\n")); err != nil {
		t.Fatalf("InitEnv() = %v, want nil", err)
	}
	if got := GetEnvValue(key); got != "from-environment" {
		t.Errorf("GetEnvValue(%s) = %q, want the environment's value to win", key, got)
	}
}

func TestInitEnvSetsAVariableThatWasNotAlreadyInTheEnvironment(t *testing.T) {
	const key = "ARTEMIS_TEST_FRESH"
	if _, ok := os.LookupEnv(key); ok {
		t.Skipf("%s is already set in this environment", key)
	}
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	if err := InitEnv(writeEnv(t, key+"=from-file\n")); err != nil {
		t.Fatalf("InitEnv() = %v, want nil", err)
	}
	if got := GetEnvValue(key); got != "from-file" {
		t.Errorf("GetEnvValue(%s) = %q, want %q", key, got, "from-file")
	}
}

// A missing env file is returned as an error naming the path, not printed:
// whether it deserves a word on the terminal is the command's call, because only
// the command knows whether the user asked for that file.
func TestInitEnvMissingFileErrorsNamingThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.env")

	err := InitEnv(path)
	if err == nil {
		t.Fatal("InitEnv() = nil, want an error for a file that does not exist")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name %q", err, path)
	}
	if !strings.Contains(err.Error(), "loading env from") {
		t.Errorf("error = %q, want it to say what it was doing", err)
	}
}

func TestInitEnvUnparseableFileIsAnError(t *testing.T) {
	if err := InitEnv(writeEnv(t, "this is not an assignment\n")); err == nil {
		t.Error("InitEnv() = nil, want an error for a file that is not env syntax")
	}
}

func TestGetEnvValueOfAnUnsetKeyIsEmpty(t *testing.T) {
	if got := GetEnvValue("ARTEMIS_TEST_DEFINITELY_UNSET"); got != "" {
		t.Errorf("GetEnvValue() = %q, want the empty string", got)
	}
}
