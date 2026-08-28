// Package buildinfo carries the identity of the running binary. The values are
// injected at link time; the fallbacks below are what a plain `go build` gets.
package buildinfo

import "runtime"

var (
	// Version is the release version, set with -ldflags at build time.
	Version = "0.1.0-dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "unknown"
	// Date is the build timestamp, RFC3339, UTC.
	Date = "unknown"
)

// GoVersion reports the toolchain that built the binary.
func GoVersion() string { return runtime.Version() }

// Platform reports the target this binary was compiled for.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
