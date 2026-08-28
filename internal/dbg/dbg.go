// Package dbg is the debug channel for the whole binary. It writes to stderr so
// --debug output never contaminates a report that is being piped or redirected.
//
// Everything written here eventually goes through the redacting writer (§8), so
// this is the only sanctioned way to print internals.
package dbg

import (
	"fmt"
	"io"
	"os"
)

var (
	enabled bool
	out     io.Writer = os.Stderr
)

// SetEnabled turns debug output on or off. Called once from the root command.
func SetEnabled(v bool) { enabled = v }

// Enabled reports whether debug output is on, for callers that want to skip
// expensive formatting entirely.
func Enabled() bool { return enabled }

// SetOutput redirects debug output. Used by tests.
func SetOutput(w io.Writer) { out = w }

// Printf writes a debug line to stderr when --debug is set.
func Printf(format string, args ...any) {
	if !enabled {
		return
	}
	fmt.Fprintf(out, "debug  "+format+"\n", args...)
}
