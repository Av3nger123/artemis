// Package version is which artemis this is: the release a binary was built as,
// or, built from source, the commit it was built from.
package version

import "runtime/debug"

// Version is the release, set at build time by the release build:
//
//	go build -ldflags "-X artemis/pkg/version.Version=v0.1.0" ./cmd/cli
//
// A build without it -- go build or make build from a clone -- leaves it empty,
// and String says which commit instead.
var Version = ""

// String is the version to show: the release when there is one, otherwise "dev"
// with the commit Go stamped into the binary, marked dirty when the tree had
// uncommitted changes, so a bug report from a source build still says what was
// running.
func String() string {
	if Version != "" {
		return Version
	}
	return dev(debug.ReadBuildInfo())
}

func dev(info *debug.BuildInfo, ok bool) string {
	if !ok {
		return "dev"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev (" + rev + ")"
}
