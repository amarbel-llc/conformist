package cmd

import (
	"fmt"

	"code.linenisgreat.com/conformist/cmd/codegen"
	"github.com/spf13/cobra"
)

// newCodegenRepairCmd implements the `codegen-repair` subcommand (conformist#124):
// the generic codegen-repair lane wired by nix/linters/codegen-repair.nix as a
// whole-tree linter's repair-command.
//
// It deliberately does NOT load a conformist.toml. It has no use for one — its
// whole input is the flake's `checks.<system>` and the git worktree — and staying
// config-free is what lets the same code be delivered as the standalone static
// artifact the RFC-0005 profile will carry at POC v2 (conformist#124 decision 4),
// where there may be no conformist config at all.
func newCodegenRepairCmd() *cobra.Command {
	opts := codegen.Options{}

	cmd := &cobra.Command{
		Use:   "codegen-repair",
		Short: "Apply every flake check's passthru.codegenPatch to the working tree",
		Long: "Find every `checks.<system>.<name>` in this repo's flake that carries " +
			"`passthru.codegenPatch` (the igloo#80 contract), build each one's patch, and apply it " +
			"to the working tree with `git apply -p2`. This restamps generated files — a tommy " +
			"header, a facade export — at commit time, so a producer bump never reaches the " +
			"repo's drift gate as a stale output.\n\n" +
			"It is generic: it knows nothing about any particular generator, and needs only nix " +
			"(from PATH, so the host's store and eval cache are used) and git. It is also " +
			"repair-only — the drift check that carries the passthru stays the gate, and " +
			"`conformist check` does not run this.\n\n" +
			"Discovery and per-check build failures, and running outside a git worktree (nothing " +
			"to apply a patch to), WARN and exit 0, because this runs in a git " +
			"pre-commit hook where a non-zero exit blocks the commit and an unreachable nix must " +
			"not block a whole repo; the drift check still catches any staleness that results. A " +
			"patch that was built but will not apply exits 2 — it was generated from this very " +
			"tree, so a refusal is a real inconsistency. --strict makes the warnings fatal too.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			opts.Stdout = cmd.OutOrStdout()

			if err := codegen.Run(cmd.Context(), opts); err != nil {
				return fmt.Errorf("codegen-repair: %w", err)
			}

			return nil
		},
	}

	fs := cmd.Flags()

	fs.StringVar(
		&opts.FlakeRef, "flake", "",
		"The flake whose checks.<system> is enumerated. Defaults to \".\", which nix resolves to "+
			"the enclosing git worktree.",
	)

	fs.StringVar(
		&opts.System, "system", "",
		"The nix system double selecting the checks attrset. Defaults to this binary's own "+
			"platform (e.g. x86_64-linux); override it for a host whose nix reports a different "+
			"system than the binary's architecture implies.",
	)

	fs.StringVar(
		&opts.Nix, "nix", "",
		"The nix executable to run. Defaults to \"nix\" from PATH — the HOST nix, so its store, "+
			"substituters and eval cache are the ones used.",
	)

	fs.StringVar(
		&opts.TreeRoot, "tree-root", "",
		"Directory to run nix in and apply patches from. Defaults to the git worktree's toplevel, "+
			"which is where a codegenPatch's paths are rooted; overriding it with anything else "+
			"will apply the patch at the wrong prefix.",
	)

	fs.BoolVar(
		&opts.Strict, "strict", false,
		"Exit non-zero when discovery fails or a check's patch cannot be built, instead of warning "+
			"and proceeding. For a gate that would rather stop than let a possibly-stale output "+
			"through.",
	)

	fs.BoolVar(
		&opts.ListOnly, "list", false,
		"Print the discovered checks as JSON (name plus its passthru.codegenIncludes) and apply "+
			"nothing. The operator diagnostic, and the discovery-cost measurement's subject.",
	)

	return cmd
}
