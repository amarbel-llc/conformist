{
  description = "an amarbel-llc repo formatted and linted by conformist";

  inputs = {
    # conformist provides the linter/formatter multiplexer, its Nix module
    # library (conformist.lib), and the eng-convention presets. Following its
    # nixpkgs-master/utils pins keeps this flake's closure shared with conformist's.
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    nixpkgs.follows = "conformist/nixpkgs-master";
    utils.follows = "conformist/utils";

    # just-us is the eng `just` fork. It supplies BOTH the devShell's `just` and
    # the justfile-orphan-summary linter module: that check reads the fork-only
    # `doc_prelude` field of `just --dump --dump-format json`, which upstream
    # `just` never emits, so running it against `pkgs.just` would pass
    # vacuously. The coupling lives in just-us rather than conformist because
    # conformist must stay strictly upstream of its consumers. Its nixpkgs /
    # flake-utils / conformist inputs follow this flake's so the closure stays
    # shared; its `bats` input deliberately keeps its own pin (see just-us's
    # flake.nix) and is unused here.
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
    just-us.inputs.nixpkgs.follows = "nixpkgs";
    just-us.inputs.flake-utils.follows = "utils";
    just-us.inputs.conformist.follows = "conformist";
  };

  outputs =
    {
      self,
      conformist,
      just-us,
      nixpkgs,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        # `.default` is the bga (buildGoApplication) build on every system —
        # platform-agnostic, no ca-derivations needed. (`.conformist-native` is an
        # opt-in godyn build, x86_64-linux only.)
        conformistPkg = conformist.packages.${system}.default;

        # The fork's `just`: the devShell's command runner AND the binary EVERY
        # justfile-* linter invokes. Never substitute `pkgs.just` — those checks
        # read fork-only parser data, so a stock `just` either rejects the format
        # outright or reads as zero findings.
        justPkg = just-us.packages.${system}.default;

        # Pure lane: the eng preset + the just-us justfile roster + this repo's own
        # formatters/excludes (./conformist.nix). Drives `nix fmt` and the sandboxed
        # `checks.formatting`.
        eval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            # The justfile-convention linters ship from just-us, NOT conformist.
            # They read `just --dump --dump-format model` (and, for the
            # orphaned-summary rule, `doc_prelude`) — fork-only parser data — so
            # the coupling lives in the repo that owns the parser, and conformist
            # stays strictly upstream of it. conformist-justfile(7) remains the
            # normative prose home for the rules themselves.
            just-us.lib.conformistPresets.justfile
            ./conformist.nix
          ];
          package = conformistPkg;

          # ONE setting for the whole justfile-linter family: every rule in the
          # roster reads it. It MUST be the fork.
          linters.justfile-common.justPackage = justPkg;
        };

        # Impure lane: the git-state checks (git-remotes, sweatfile, agents-md).
        # They need a live .git / host tools, so they run via `just lint-worktree`
        # against the working tree, not the sandboxed check. Wire this in once the
        # repo has SSH remotes + a sweatfile (see the justfile).
        impureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        formatter = eval.config.build.wrapper;
        checks.formatting = eval.config.build.check self;
        packages.conformist-impure-config = impureEval.config.build.configFile;

        # A git pre-commit hook running THIS repo's config (conformist#47/#51):
        # `conformist --staged --exit-zero-on-fix` wrapped with the generated
        # store-path config, so it formats staged files with the SAME pinned
        # toolchain as `nix fmt` — no reliance on the formatters being on the
        # author's PATH (the silent-skip trap of a bare `conformist --staged`).
        # It is on the devShell PATH as `conformist-pre-commit`; wire the
        # sweatfile hook to that name (see ./sweatfile).
        packages.conformist-pre-commit = eval.config.build.preCommit;

        # The merge-time repair sibling (conformist#54): `conformist --commit
        # --amend --exit-zero-on-fix`, same store-pinned toolchain, on the
        # devShell PATH as `conformist-repair`. It is the supported command for a
        # spinclass pre-merge REPAIR hook. Exposed and available here; the
        # sweatfile leaves `repair = "conformist-repair"` as a documented opt-in,
        # since with the per-commit hook active the tree is already conformant at
        # merge time (see ./sweatfile).
        packages.conformist-repair = eval.config.build.repair;

        devShells.default = pkgs.mkShell {
          packages = [
            conformistPkg
            # The config-specific, toolchain-hermetic hooks, on PATH as
            # `conformist-pre-commit` / `conformist-repair` so the sweatfile can
            # name them.
            eval.config.build.preCommit
            eval.config.build.repair
            justPkg
          ];
        };
      }
    );
}
