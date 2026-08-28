package main

import (
	"os"

	"github.com/prabhuvmk/aispend/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// cobra has already written the message to stderr.
		os.Exit(1)
	}
}
