package main

import (
	"os"

	"code.linenisgreat.com/conformist/cmd"
)

// version and commit are injected at build time. The amarbel-llc/nixpkgs fork's
// buildGoApplication sets -X main.version (from version.env) and -X main.commit
// (from the flake's self.rev); a plain `go build` leaves the defaults below.
// See eng-versioning(7).
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	root, _ := cmd.NewRoot(version, commit)
	os.Exit(cmd.ExitCode(root.Execute()))
}
