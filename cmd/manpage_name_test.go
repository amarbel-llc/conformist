package cmd_test

import (
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/cmd"
	"github.com/spf13/cobra"
)

// maxNameLine is the fleet NAME-line contract (doppelgang lint-man --max 72): the
// whole "name - summary" line fits in 72 columns, so it renders untruncated in a
// manpage index (conformist#129).
const maxNameLine = 72

// TestGeneratedManpageNameLinesFitTheFleetContract checks the line cobra's
// GenManTree writes under NAME for every generated page: the command path with
// spaces as dashes, " - ", then the command's Short. The name counts toward the
// limit, so a longer command path leaves a shorter Short.
func TestGeneratedManpageNameLinesFitTheFleetContract(t *testing.T) {
	root, _ := cmd.NewRoot("test", "test")

	var walk func(c *cobra.Command)

	checked := 0

	walk = func(c *cobra.Command) {
		// GenManTree skips what is not available (hidden, deprecated).
		if !c.IsAvailableCommand() && c != root {
			return
		}

		line := strings.ReplaceAll(c.CommandPath(), " ", "-") + " - " + c.Short
		if len(line) > maxNameLine {
			t.Errorf("NAME line is %d chars (max %d): %q", len(line), maxNameLine, line)
		}

		if strings.ContainsAny(c.Short, "\n") {
			t.Errorf("%s: Short spans lines: %q", c.CommandPath(), c.Short)
		}

		checked++

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)

	// Positive control: the walk reached the subcommands, not just the root.
	if checked < 5 {
		t.Fatalf("walk checked only %d commands; subcommands were not reached", checked)
	}
}
