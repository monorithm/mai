// Package version holds the version of the CLI. The version is also the tag of the server
// image that the CLI runs.
//
// "go install github.com/monorithm/mai/cli/v3/cmd/mai@v3.0.0" records the module version
// v3.0.0 in the build information, and this package reads it from there. A build with
// -ldflags "-X .../internal/version.Version=X.Y.Z" sets the version directly. Each other
// build (a local build, or a pseudo-version) keeps the development version.
package version

import (
	"regexp"
	"runtime/debug"
)

// Dev is the version of a build that has no release version.
const Dev = "3.2.0-dev"

// Version is the version of the CLI and of the image that it runs.
var Version = Dev

// release matches only a release version of a module, not a pseudo-version or a dirty build.
var release = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

// fromModule returns the release version in the module version v, or "" if v is not one.
func fromModule(v string) string {
	if m := release.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return ""
}

func init() {
	if Version != Dev {
		return // -ldflags set it
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := fromModule(bi.Main.Version); v != "" {
			Version = v
		}
	}
}
