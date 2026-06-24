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
	var (
		noEdit         bool
		forceFormatter bool
	)

	cmd := &cobra.Command{
		Use:   "conform",
		Short: "Scaffold this repo into the amarbel-llc conformist shape",
		Long: "Transition the current repo toward the amarbel-llc conformist shape. Writes " +
			"each shape file that is absent — conformist.nix, a version.env keyed to the repo " +
			"name, a sweatfile wiring conformist's pre-commit and (opt-in) repair hooks, and " +
			"(for a greenfield repo) a complete flake.nix and justfile. An existing flake.nix " +
			"that is the recognized eachDefaultSystem shape is edited IN PLACE to add the " +
			"conformist input and the per-system outputs wiring; any other shape (or --no-edit) " +
			"falls back to printing the wiring to paste. An existing justfile is never edited; " +
			"its recipes are printed. An existing formatter is left as a conflict unless " +
			"--force-formatter replaces it with conformist's wrapper. It is idempotent. Exits 0 " +
			"when nothing changed, 3 when it wrote or edited files.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to resolve working directory: %w", err)
			}

			res, err := conform.Run(dir, cmd.OutOrStdout(), conform.Options{
				NoEdit:         noEdit,
				ForceFormatter: forceFormatter,
			})
			if err != nil {
				return fmt.Errorf("conform: %w", err)
			}

			if res.Changed() {
				return ErrScaffolded
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&noEdit, "no-edit", false,
		"do not edit an existing flake.nix in place; print the wiring to paste instead")
	cmd.Flags().BoolVar(&forceFormatter, "force-formatter", false,
		"replace an existing flake.nix `formatter` with conformist's wrapper instead of reporting a conflict")

	return cmd
}
