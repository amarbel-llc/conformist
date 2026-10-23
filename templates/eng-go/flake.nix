{
  description = "an amarbel-llc repo formatted and linted by conformist";

  inputs = {
    # conformist provides the linter/formatter multiplexer, its Nix module
    # library (conformist.lib), and the eng-convention presets. Following its
    # nixpkgs-master/utils pins keeps this flake's closure shared with conformist's.
    conformist.url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
    conformist.inputs.igloo.follows = "igloo";
    nixpkgs.follows = "conformist/nixpkgs-master";
    utils.follows = "conformist/utils";

    # just-us is the eng `just` fork. It supplies BOTH the devShell's `just` and
    # the justfile-orphan-summary linter module: that check reads the fork-only
    # `doc_prelude` field of `just --dump --dump-format json`, which upstream
    # `just` never emits, so running it against `pkgs.just` would pass
    # vacuously. The coupling lives in just-us rather than conformist because
    # conformist must stay strictly upstream of its consumers. Its nixpkgs /
    # flake-utils / conformist / igloo inputs follow this flake's so the closure
    # stays shared; its `bats` input deliberately keeps its own pin (see just-us's
    # flake.nix) and is unused here.
    just-us.url = "https://code.linenisgreat.com/just-us/archive/master.tar.gz";
    just-us.inputs.nixpkgs.follows = "nixpkgs";
    just-us.inputs.flake-utils.follows = "utils";
    just-us.inputs.conformist.follows = "conformist";
    just-us.inputs.igloo.follows = "igloo";

    # igloo carries the Go build helpers: godyn (buildGoAuto, the go.nix
    # manifest, godyn-go / godyn-test) and mkGoPkgs. See godyn(7).
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs";

    # purse-first ships dagnabit, the package-layout tool behind `checks.layout`
    # (internal/<level>/<leaf> must sit at the level its imports compute).
    # doppelgang ships `lint-man`, the NAME-line check behind
    # `checks.manpage-names`. Both depend on conformist, so they are consumers
    # here the way this flake is; conformist itself takes neither (that would
    # be a cycle). Follows keep their closures shared with this flake's pins.
    purse-first.url = "https://code.linenisgreat.com/purse-first/archive/master.tar.gz";
    purse-first.inputs.nixpkgs-master.follows = "nixpkgs";
    purse-first.inputs.utils.follows = "utils";
    purse-first.inputs.igloo.follows = "igloo";
    purse-first.inputs.conformist.follows = "conformist";

    doppelgang.url = "https://code.linenisgreat.com/doppelgang/archive/master.tar.gz";
    doppelgang.inputs.nixpkgs-master.follows = "nixpkgs";
    doppelgang.inputs.utils.follows = "utils";
    doppelgang.inputs.igloo.follows = "igloo";
    doppelgang.inputs.conformist.follows = "conformist";
  };

  outputs =
    inputs@{
      self,
      conformist,
      just-us,
      nixpkgs,
      utils,
      igloo,
      purse-first,
      doppelgang,
      ...
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        # igloo's pkgs carry everything plain nixpkgs does, plus the godyn
        # overlay (buildGoAuto, godyn-go, godyn-test) used below.
        pkgs = import igloo { inherit system; };

        # eng-versioning(7): version.env is the single source of truth. Matching
        # on the `_VERSION=` suffix keeps this line independent of the repo name.
        appVersion = builtins.head (
          builtins.match ".*_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
        );
        appCommit = self.shortRev or self.dirtyShortRev or "unknown";

        # godyn build (igloo FDR 0008, godyn(7)): dependencies live in
        # ./go.nix (no tracked go.mod/gomod2nix.toml, no ambient go); go.mod
        # and gomod2nix.toml are rendered from it inside nix.
        app = pkgs.buildGoAuto {
          pname = "EXAMPLE_NAME";
          version = appVersion;
          inherit inputs;
          src = ./.;
          manifest = ./go.nix;
          # The module root is the main package. A repo that grows cmd/<name>/
          # lists those directories here instead.
          subPackages = [ "." ];
          # godyn's per-package test lane (passthru.checkAll).
          tests = true;
          nativeArgs = {
            commit = appCommit;
          };
          bgaArgs = {
            commit = appCommit;
            GOTOOLCHAIN = "local";
          };
        };

        # Section-1/7 manpages: doc/*.N.scd compiled by scdoc into
        # share/man/manN. doc/cli.1.scd is installed under the package's name, so
        # the template needs no file named after the repo.
        manpages = pkgs.runCommand "EXAMPLE_NAME-manpages" { nativeBuildInputs = [ pkgs.scdoc ]; } ''
          for src in ${./doc}/*.scd; do
            base=$(basename "$src" .scd)
            section=''${base##*.}
            name=''${base%.*}
            [ "$name" = cli ] && name=EXAMPLE_NAME
            mkdir -p "$out/share/man/man$section"
            scdoc < "$src" > "$out/share/man/man$section/$name.$section"
          done
        '';

        # The binary plus its manpages: what `nix build` and an installer get.
        installed = pkgs.symlinkJoin {
          name = "EXAMPLE_NAME-${appVersion}";
          paths = [
            app
            manpages
          ];
          meta.mainProgram = "EXAMPLE_NAME";
        };

        # `.default` is the bga (buildGoApplication) build on every system —
        # platform-agnostic, no ca-derivations needed. (`.conformist-native` is an
        # opt-in godyn build, x86_64-linux only.)
        conformistPkg = conformist.packages.${system}.default;

        # The fork's `just`: the devShell's command runner AND the binary EVERY
        # justfile-* linter invokes. Never substitute `pkgs.just` — those checks
        # read fork-only parser data, so a stock `just` either rejects the format
        # outright or reads as zero findings.
        justPkg = just-us.packages.${system}.default;

        # Pure lane: the eng + eng-go presets (goimports then gofumpt) + the
        # just-us justfile roster + this repo's own formatters/excludes
        # (./conformist.nix). Drives `nix fmt` and the sandboxed
        # `checks.formatting`.
        eval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            conformist.lib.presets.eng-go
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
        # plus this repo's impure overlay (./conformist-impure.nix, where
        # `conformist conform --allow-host` persists the git-remotes allowlist).
        # They need a live .git / host tools, so they run via `just lint-worktree`
        # against the working tree, not the sandboxed check.
        impureEval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng-impure
            ./conformist-impure.nix
          ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        packages.default = installed;
        packages.EXAMPLE_NAME = app;
        packages.manpages = manpages;

        formatter = eval.config.build.wrapper;
        checks.formatting = eval.config.build.check self;
        # godyn's per-package test lane from go.nix. `just test-go` builds this.
        checks.tests = app.passthru.checkAll or app;

        # internal/<level>/<leaf> must sit at the level dagnabit computes from
        # its imports (`just lint-layout`; AGENTS.md, LAYOUT). A dry run that
        # fails on any would-move line: applying a move needs `git mv`, and the
        # rendered tree is not a git checkout. Move the package by hand to the
        # level shown and fix the import paths. conformist.lib.mkLayoutCheck takes
        # dagnabit as a parameter (conformist cannot depend on purse-first).
        checks.layout = conformist.lib.mkLayoutCheck {
          inherit app;
          dagnabit = purse-first.packages.${system}.dagnabit;
        };

        # Every installed page must satisfy the fleet NAME-line contract: the
        # pages render into session system prompts' manpage indexes, where a
        # wrapped or oversized NAME line degrades the whole index.
        checks.manpage-names =
          pkgs.runCommand "EXAMPLE_NAME-manpage-names"
            {
              nativeBuildInputs = [ doppelgang.packages.${system}.default ];
            }
            ''
              doppelgang lint-man --format text --max 72 ${manpages}/share/man
              touch $out
            '';
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
        # spinclass pre-merge REPAIR hook (see ./sweatfile).
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
            # No ambient go: dependencies live in go.nix (igloo FDR 0008); go
            # commands run through godyn-go, tests through godyn-test.
            pkgs.godyn-go
            pkgs.godyn-test
          ];
        };
      }
    );
}
