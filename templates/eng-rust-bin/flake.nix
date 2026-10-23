{
  description = "an amarbel-llc repo formatted and linted by conformist";

  inputs = {
    # conformist provides the linter/formatter multiplexer, its Nix module
    # library (conformist.lib), and the eng-convention presets. Following its
    # nixpkgs-master/utils pins keeps this flake's closure shared with conformist's.
    conformist.url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
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

    # doppelgang ships `lint-man`, the NAME-line check behind
    # `checks.manpage-names`. It depends on conformist, so it is a consumer here
    # the way this flake is; conformist itself does not take it (a cycle).
    doppelgang.url = "https://code.linenisgreat.com/doppelgang/archive/master.tar.gz";
    doppelgang.inputs.nixpkgs-master.follows = "nixpkgs";
    doppelgang.inputs.utils.follows = "utils";
    doppelgang.inputs.conformist.follows = "conformist";
  };

  outputs =
    {
      self,
      conformist,
      doppelgang,
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

        # Impure lane: the git-state checks (git-remotes, sweatfile, agents-md)
        # plus this repo's clippy linter (./conformist-impure.nix). They need a
        # live .git / a real Rust toolchain, so they run via `just lint-worktree`
        # against the working tree, not the sandboxed check.
        impureEval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng-impure
            ./conformist-impure.nix
          ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };

        # Manpages: doc/<name>.<section>.scd compiled by scdoc into
        # share/man/man<section>. cli.N.scd and lib.N.scd are installed under the
        # crate's name (read from Cargo.toml), so no file is named after the repo.
        # The package templates ship a page; the page-less base has no doc/, and
        # this one flake is shared across all three (a drift test keeps them equal).
        hasDocs = builtins.pathExists ./doc;
        crateName = (builtins.fromTOML (builtins.readFile ./Cargo.toml)).package.name;
        manpages = pkgs.runCommand "${crateName}-manpages" { nativeBuildInputs = [ pkgs.scdoc ]; } ''
          for src in ${./doc}/*.scd; do
            base=$(basename "$src" .scd)
            section=''${base##*.}
            name=''${base%.*}
            case "$name" in cli | lib) name=${crateName} ;; esac
            mkdir -p "$out/share/man/man$section"
            scdoc < "$src" > "$out/share/man/man$section/$name.$section"
          done
        '';
      in
      {
        formatter = eval.config.build.wrapper;

        # Every installed page must satisfy the fleet NAME-line contract (one-line
        # `name - summary`, short enough to render untruncated); doppelgang
        # lint-man is the reference implementation. Trivially passes without doc/.
        checks.manpage-names =
          if hasDocs then
            pkgs.runCommand "manpage-names" { nativeBuildInputs = [ doppelgang.packages.${system}.default ]; }
              ''
                doppelgang lint-man --format text --max 72 ${manpages}/share/man
                touch $out
              ''
          else
            pkgs.runCommand "manpage-names" { } "touch $out";
        packages.manpages = if hasDocs then manpages else pkgs.runCommand "no-manpages" { } "mkdir $out";
        checks.formatting = eval.config.build.check self;
        packages.conformist-impure-config = impureEval.config.build.configFile;

        # The pinned conformist binary, for `nix run '.#conformist'`. The
        # lint-worktree recipe runs it by flake reference instead of trusting PATH,
        # where eng's home-profile `conformist` wrapper (it gates on a checked-in
        # config this repo does not have) can win over the devShell's (#131).
        packages.conformist = conformistPkg;

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
            # The Rust toolchain the justfile's `test-rust` (cargo test) needs on
            # PATH; gcc is the linker cargo invokes. The clippy lane bakes its own
            # copy (conformist-impure.nix), so this is for interactive use and
            # `just`, not the lint gate.
            pkgs.cargo
            pkgs.rustc
            pkgs.clippy
            pkgs.rustfmt
            pkgs.gcc
          ];
        };
      }
    );
}
