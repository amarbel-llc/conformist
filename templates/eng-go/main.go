// Command EXAMPLE_NAME is the scaffold's entry point: it prints its version.
package main

import (
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/conformist/templates/eng-go/internal/0/buildinfo"
)

// Injected at link time by the nix build (godyn(7), VERSION EMBEDDING).
var (
	version = "dev"
	commit  = "unknown"
)

func versionLine() string {
	return buildinfo.Line("EXAMPLE_NAME", version, commit)
}

func run(out io.Writer) error {
	_, err := fmt.Fprintln(out, versionLine())

	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
