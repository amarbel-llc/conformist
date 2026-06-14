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
			"conformist.nix and version.env if they are absent (skipping any that exist), " +
			"then prints the flake.nix and justfile wiring to add by hand. It is idempotent " +
			"and never edits existing files — auto-rewriting an arbitrary flake.nix is fragile, " +
			"so conform scaffolds the new pieces and reports the rest. Exits 0 when nothing was " +
			"written, 3 when it scaffolded files.",
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
