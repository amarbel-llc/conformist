package cmd

import (
	"fmt"

	"code.linenisgreat.com/conformist/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newIdentityCmd implements the `identity` subcommand (conformist#76). It prints
// the resolved config/toolchain identity hash for the current tree, so a wrapper
// or hook can assert that an invocation matches the repo's canonical config
// before letting it rewrite the tree — the enforcement half of the attestation
// that the format/repair path records and checks automatically.
func newIdentityCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "identity",
		Short: "Print the resolved config/toolchain identity hash",
		Long: "Print a stable hex hash of the resolved configuration that determines how " +
			"this tree is formatted and linted — the formatter/linter tables (including each " +
			"tool's command, a /nix/store path for a Nix-module config, which encodes its " +
			"version) plus the global excludes and walk type. conformist records this identity " +
			"after a successful format/repair run and warns when a later run's identity differs " +
			"(see --refuse-identity-mismatch); this command exposes it so wrappers and hooks " +
			"can assert an invocation matches the repo's canonical config (conformist#76).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			workingDir, err := changeWorkingDir(v)
			if err != nil {
				return err
			}

			if err := loadConfig(v, cmd, workingDir); err != nil {
				return err
			}

			cfg, err := config.FromViper(v)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if _, err := fmt.Fprintln(cmd.OutOrStdout(), cfg.Identity()); err != nil {
				return fmt.Errorf("failed to write identity: %w", err)
			}

			return nil
		},
	}
}
