package cli

import (
	"testing"

	"artemis/pkg/version"
)

// Both spellings print the same one line, the release a build was stamped with.
func TestVersionAndTheVersionFlagAgree(t *testing.T) {
	initOnce.Do(Init)
	stamp := func(v string) {
		version.Version = v
		RootCmd.Version = version.String()
		RootCmd.SetVersionTemplate(versionLine() + "\n")
	}
	stamp("v0.1.0")
	t.Cleanup(func() {
		stamp("")
		// A flag set once stays set on RootCmd, and every later Execute in
		// this package would print the version instead of running.
		if f := RootCmd.Flags().Lookup("version"); f != nil {
			_ = f.Value.Set("false")
			f.Changed = false
		}
	})
	for _, args := range [][]string{{"version"}, {"--version"}} {
		out, _, err := executeArgs(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != "artemis v0.1.0\n" {
			t.Errorf("%v printed %q, want %q", args, out, "artemis v0.1.0\n")
		}
	}
}
