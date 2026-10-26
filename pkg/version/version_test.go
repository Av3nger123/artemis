package version

import (
	"runtime/debug"
	"testing"
)

func TestStringIsTheReleaseWhenThereIsOne(t *testing.T) {
	t.Cleanup(func() { Version = "" })
	Version = "v0.1.0"
	if got := String(); got != "v0.1.0" {
		t.Errorf("String() = %q, want the release v0.1.0", got)
	}
}

func TestASourceBuildNamesItsCommit(t *testing.T) {
	info := func(settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Settings: settings}
	}
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "7c93bc0a1b2c3d4e5f60718293a4b5c6d7e8f901"}
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "dev"},
		{"no commit", info(), true, "dev"},
		{"clean", info(rev, debug.BuildSetting{Key: "vcs.modified", Value: "false"}), true, "dev (7c93bc0a1b2c)"},
		{"dirty", info(rev, debug.BuildSetting{Key: "vcs.modified", Value: "true"}), true, "dev (7c93bc0a1b2c-dirty)"},
	} {
		if got := dev(tc.info, tc.ok); got != tc.want {
			t.Errorf("%s: dev() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
