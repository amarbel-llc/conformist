package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/amarbel-llc/conformist/cmd/conform"
	"github.com/spf13/cobra"
)

// ErrScaffolded indicates `conformist conform` created one or more files (the
// tree changed); ExitCode maps it to 3, matching repair mode's "fixes applied".
var ErrScaffolded = errors.New("scaffolded conformist files")

func newConformCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "conform",
		Short: "Scaffold this repo into the amarbel-llc conformist shape",
		Long: "Transition the current repo toward the amarbel-llc conformist shape. Writes " +
			"each shape file that is absent — conformist.nix, version.env, and (for a greenfield " +
			"repo) a complete flake.nix and justfile — skipping any that already exist. It is " +
			"idempotent and never edits an existing file: when flake.nix or justfile is already " +
			"present, conform leaves it untouched and prints the wiring to paste instead, since " +
			"auto-rewriting an arbitrary flake.nix is fragile. Exits 0 when nothing was written, " +
			"3 when it scaffolded files.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to resolve working directory: %w", err)
			}

			res, err := conform.Run(dir, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("conform: %w", err)
			}

			if len(res.Wrote) > 0 {
				return ErrScaffolded
			}

			return nil
		},
	}
}
