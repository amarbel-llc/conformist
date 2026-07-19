package cmd_test

import (
	"io"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd"
)

// TestRootAcceptsPositionalPathArgs guards against a regression where adding
// subcommands (check, version) made cobra reject `conformist <path>` as an
// "unknown command" instead of passing the path to the root RunE. The command
// is expected to fail here (no config file), but the failure must not be a
// subcommand-resolution error.
func TestRootAcceptsPositionalPathArgs(t *testing.T) {
	root, _ := cmd.NewRoot("test", "test")
	root.SetArgs([]string{"some/nonexistent/path"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	if err := root.Execute(); err != nil && strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("positional path arg was rejected as a subcommand: %v", err)
	}
}
