package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/prabhuvmk/aispend/internal/cli"
	"github.com/prabhuvmk/aispend/internal/redact"
)

func main() {
	// A panic prints a struct dump and a stack trace, and either can carry a
	// credential a caller was holding. The Go runtime writes that straight to
	// file descriptor 2, where an io.Writer wrapper cannot reach it — so the
	// panic is recovered here and re-printed through the redacting writer
	// instead.
	defer func() {
		if r := recover(); r != nil {
			out := redact.New(os.Stderr)
			fmt.Fprintf(out, "\naispend crashed: %v\n\n", r)
			out.Write(debug.Stack())
			fmt.Fprintf(out, "\nThis is a bug. Please report it with the output above.\n")
			os.Exit(2)
		}
	}()

	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
