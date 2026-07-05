package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/amarbel-llc/conformist/cmd/conform"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ErrScaffolded indicates `conformist conform` created, edited, or repaired one
// or more files (the tree changed); ExitCode maps it to 3, matching repair
// mode's "fixes applied".
var ErrScaffolded = errors.New("scaffolded conformist files")

// ErrConformFailed indicates an operational failure of `conform` (template
// resolution, an ambiguous/missing selection, a refused non-empty target, or a
// failed repair/init); ExitCode maps it to 2, matching the binary's "2 on
// operational error" convention.
var ErrConformFailed = errors.New("conform failed")

func newConformCmd() *cobra.Command {
	var (
		noEdit         bool
		forceFormatter bool
		repair         bool
		overwrite      bool
	)

	cmd := &cobra.Command{
		Use:   "conform [<domain>[#<id>]]",
		Short: "Scaffold this repo into the amarbel-llc conformist shape, or bootstrap one from a domain's PAPI template",
		Long: "With no argument, transition the current repo toward the amarbel-llc conformist shape. Writes " +
			"each absent shape file — conformist.nix, a version.env keyed to the repo name, a sweatfile, and " +
			"(greenfield) a complete flake.nix and justfile. An existing flake.nix that is the recognized " +
			"eachDefaultSystem shape is edited IN PLACE to add the conformist wiring; any other shape (or " +
			"--no-edit) falls back to printing the wiring. A flake.nix/justfile that already carries the " +
			"conformist wiring is detected and left silent. It then prints the single command that brings the " +
			"working tree fully up to spec by delegating to conformist's own repair linters; --repair runs that " +
			"command inline (format + eng-lint repair over the working tree, no commit).\n\n" +
			"With a <domain> (or <domain>#<id>) argument, bootstrap a NEW repo from a flake template advertised " +
			"by that domain's PAPI document (PAPI RFC-0001 §7/§8): it resolves the template, surfaces the " +
			"resolved flakeref, and runs `nix flake init -t <flakeref>` in the current directory. A bare domain " +
			"with one template uses it; with several it prompts on a TTY and otherwise fails listing the ids.\n\n" +
			"It is idempotent. Local mode exits 0 when nothing changed and 3 when it wrote/edited/repaired " +
			"files; bootstrap exits 0 on success; 2 on an operational error.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to resolve working directory: %w", err)
			}

			// Domain bootstrap mode (#43): `conform <domain>` / `<domain>#<id>`.
			if len(args) == 1 {
				return runBootstrap(cmd, args[0], dir, overwrite)
			}

			// Local scaffold + convergence mode (#41/#42).
			res, err := conform.Run(dir, cmd.OutOrStdout(), conform.Options{
				NoEdit:         noEdit,
				ForceFormatter: forceFormatter,
				Repair:         repair,
			})
			if err != nil {
				return fmt.Errorf("%w: %w", ErrConformFailed, err)
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
	cmd.Flags().BoolVar(&repair, "repair", false,
		"after scaffolding, run the working-tree repair (format + eng-lint repair, no commit) inline "+
			"instead of only printing the command")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false,
		"in <domain> bootstrap mode, allow `nix flake init` to scaffold into a non-empty directory")

	return cmd
}

// runBootstrap dispatches the `conform <domain>` template-bootstrap mode (#43),
// mapping an interactive terminal to the huh chooser and any failure to
// ErrConformFailed (exit 2).
func runBootstrap(cmd *cobra.Command, target, dir string, overwrite bool) error {
	err := conform.Bootstrap(cmd.Context(), target, dir, cmd.OutOrStdout(), conform.BootstrapOptions{
		Overwrite:   overwrite,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConformFailed, err)
	}

	return nil
}
