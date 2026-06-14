{
  description = "conformist: the linter and formatter multiplexer";

  inputs = {
    # amarbel-llc/nixpkgs fork. Its overlay carries a patched
    # buildGoApplication that auto-injects `-X main.version` (read from
    # version.env in the module dir) and `-X main.commit` (from src.rev),
    # so no per-repo ldflags wiring is needed. See eng-versioning(7) and
    # amarbel-llc/nixpkgs#31.
    igloo.url = "github:amarbel-llc/igloo";
    # Apply igloo's overlay to OUR pinned nixpkgs-master (the hydra-vetted
    # sha eng pins) instead of igloo's own committed copy of the same pin.
    # Only igloo's flake-path outputs honor this — `pkgs` below therefore
    # uses `igloo.legacyPackages`, not the `import igloo {}` shim, which
    # reads igloo's committed flake.lock and is follows-immune (igloo#37).
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";

    # Pinned plain nixpkgs, source of the Go dev tooling in the devShell
    # (gofumpt/golangci-lint/gopls). The Go *toolchain* itself now comes from
    # igloo's `pkgs.go` so the buildGoApplication and native (godyn) backends
    # share one compiler — see igloo#29 / buildGoAuto.
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";

    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";

    # Source of the custom golangci-lint built with dewey's gclplugin
    # (purse-first#134): `packages.<system>.golangci-lint-dewey`, a complete
    # golangci-lint v2.12.2 with the `dewey` module-plugin analyzers registered.
    # conformist consumes it for the dewey lint lane (conformist#10).
    purse-first.url = "github:amarbel-llc/purse-first";
    # Mirror eng's follows on purse-first (igloo/nixpkgs-master/utils) so the
    # standalone lock matches the build-home closure and the duplicate
    # igloo/nixpkgs-master/utils subtree collapses to single nodes. Note
    # purse-first still consumes igloo via the follows-immune
    # `import igloo {}` shim (igloo#37), so the igloo follows redirects which
    # igloo *tree* it builds with (whose committed lock pins the nixpkgs
    # sha), not the sha directly.
    purse-first.inputs.igloo.follows = "igloo";
    purse-first.inputs.nixpkgs-master.follows = "nixpkgs-master";
    purse-first.inputs.utils.follows = "utils";
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
      purse-first,
    }:
    let
      # conformist's own Nix module library (issue #4). Exposed as `self.lib` so
      # downstream flakes can `conformist.lib.evalModule pkgs { ... }`, and
      # consumed below for conformist's own `nix fmt` / `checks.formatting`
      # (self-consumption — conformist no longer depends on treefmt-nix).
      conformistLib = import ./nix;
    in
    (utils.lib.eachDefaultSystem (
      system:
      let
        # igloo's flake path, NOT the `import igloo {}` shim: legacyPackages
        # builds from igloo's nixpkgs-master *input* (redirected to our pinned
        # sha by the follows above) and already applies the fork overlay +
        # allowUnfree. The shim resolves nixpkgs from igloo's committed
        # flake.lock and would silently ignore the follows (igloo#37).
        pkgs = igloo.legacyPackages.${system};
        pkgs-master = import nixpkgs-master { inherit system; };

        # Per-system backend gating: godyn is only exercised on x86_64-linux
        # (godyn(7) LIMITATIONS), and the committed godyn-graph.json is generated
        # there — `go list`'s file/import selection honors GOOS/GOARCH, so the
        # graph embeds linux/amd64-only sources (e.g. x/sys/unix's *_linux*.go +
        # asm_linux_amd64.s) that cannot compile on any other system. Until
        # igloo#33 (per-system graphs) lands, every other system defaults to the
        # bga backend; the godyn outputs stay exposed for opt-in.
        godynSystem = system == "x86_64-linux";

        # bga (buildGoApplication) build — the ca-derivations-free backend behind
        # `.#conformist-bga`, and the default backend on non-x86_64-linux systems
        # (godynSystem above; the godyn build is the default where exercised).
        conformistBin = pkgs.buildGoApplication {
          pname = "conformist";
          # `src = self` lets the fork's buildGoApplication resolve
          # `-X main.commit` from self.rev and read version.env (carried in
          # src) for `-X main.version`. version + commit are injected
          # automatically — no ldflags here.
          src = self;
          pwd = ./.;
          modules = ./gomod2nix.toml;
          subPackages = [ "." ];
          # igloo's pkgs.go (1.26.3), shared with the native (godyn) backend so
          # both build paths use one compiler (igloo#29). go.mod is `go 1.26.1`;
          # GOTOOLCHAIN = "local" pins to pkgs.go rather than fetching a toolchain.
          go = pkgs.go;
          GOTOOLCHAIN = "local";
          # Integration tests need formatter executables on PATH; run them via
          # `just test-go` / bats outside the sandbox, not in the package build.
          doCheck = false;
        };

        # Man pages, built by Nix per eng-manpages(7) PRINCIPLE 4 (not a justfile
        # recipe, not CI): scdoc compiles the hand-written section-5/7 sources in
        # doc/, and `conformist gen-man` renders the section-1 CLI reference from
        # the cobra command tree (PRINCIPLE 3). This derivation IS the man-page
        # lint — a malformed .scd fails the build. Rendered roff is never committed.
        # Man pages factory, parameterised by the conformist binary used to run
        # `gen-man` — so each backend's package bundles man pages built with its
        # own binary, without dragging in the other backend.
        mkManpages =
          bin:
          pkgs.runCommand "conformist-manpages"
            {
              nativeBuildInputs = [
                pkgs.scdoc
                bin
              ];
            }
            ''
              mkdir -p $out/share/man/man1
              # Compile every hand-written scdoc page, deriving its man section
              # from the penultimate extension (e.g. conformist.toml.5.scd ->
              # man5). Any hand-written section (2-9) ships automatically rather
              # than being silently dropped, so the build keeps acting as the
              # man-page lint. Section 1 is owned by `gen-man` (codegen) below, so
              # a stray *.1.scd is reported and skipped rather than racing gen-man
              # over the same man1 page; a misnamed file (no numeric section) is
              # likewise surfaced instead of producing a bogus man<word> dir.
              for f in ${self}/doc/*.scd; do
                [ -e "$f" ] || continue
                page=$(basename "''${f%.scd}") # e.g. conformist.toml.5
                section="''${page##*.}"         # e.g. 5
                case "$section" in
                  [2-9]) ;;
                  *)
                    echo "manpages: skipping $f (section '$section' is not a hand-written man section 2-9; section 1 is codegen)" >&2
                    continue
                    ;;
                esac
                mkdir -p "$out/share/man/man$section"
                scdoc < "$f" > "$out/share/man/man$section/$page"
              done
              # Section 1 (the CLI reference) is codegen from the cobra tree, not scdoc.
              conformist gen-man "$out/share/man/man1"
            '';

        # Man pages per backend: the default (godyn) package needs no bga build,
        # and the bga fallback needs no ca-derivations.
        manpages = mkManpages conformist-native;
        manpagesBga = mkManpages conformistBin;

        # The godyn package: the godyn (native) binary plus its man pages. The
        # shipped default on godynSystem; `nix build`, `nix run .`, and
        # `.#conformist` resolve here on x86_64-linux and to conformist-bga
        # elsewhere (see conformistDefault below). `meta.mainProgram` keeps
        # `nix run` / `lib.getExe` resolving to bin/conformist.
        conformist = pkgs.symlinkJoin {
          name = "conformist";
          paths = [
            conformist-native
            manpages
          ];
          meta = (conformist-native.meta or { }) // {
            mainProgram = "conformist";
          };
        };

        # Opt-in bga package: the single input-addressed buildGoApplication
        # derivation + bga-built man pages. ca-derivations-free, so consumers
        # without that experimental feature (or who want the cold/release-faster
        # single-derivation build) can `nix build .#conformist-bga`. See the
        # backend bench (`just debug-bench-backends`) for the tradeoffs.
        conformist-bga = pkgs.symlinkJoin {
          name = "conformist-bga";
          paths = [
            conformistBin
            manpagesBga
          ];
          meta = (conformistBin.meta or { }) // {
            mainProgram = "conformist";
          };
        };

        # The per-system default package and self-consumption binary (see
        # godynSystem above): godyn where it's exercised, bga everywhere else.
        conformistDefault = if godynSystem then conformist else conformist-bga;
        selfBin = if godynSystem then conformist-native else conformistBin;

        # Native (godyn) build of the bare binary, driven by the committed
        # godyn-graph.json (igloo#29). buildGoAuto with strategy = "dev" selects
        # igloo's per-package godyn backend (`go tool compile`/`link` directly,
        # no `go build`). This is the DEFAULT backend on x86_64-linux (godynSystem):
        # the `conformist` join above bundles it with man pages, and
        # `.#conformist-native` exposes the bare binary (no man pages) for the fast
        # inner loop and the backend bench. Its per-package outputs are
        # content-addressed, so building it requires the ca-derivations feature;
        # the input-addressed bga build is `.#conformist-bga`.
        #
        # subPackages / GOTOOLCHAIN are buildGoApplication-only knobs and so live
        # under bgaArgs (the godyn backend ignores them: its scope is the graph,
        # and it calls the toolchain directly). go = pkgs.go matches conformistBin
        # so both backends share one compiler. version/commit are auto-injected
        # from version.env + self.rev — no ldflags here.
        conformist-native = pkgs.buildGoAuto {
          pname = "conformist";
          src = self;
          graphFile = ./godyn-graph.json;
          modules = ./gomod2nix.toml;
          strategy = "dev";
          bgaArgs = {
            pwd = ./.;
            subPackages = [ "." ];
            go = pkgs.go;
            GOTOOLCHAIN = "local";
            doCheck = false;
          };
        };

        # conformist self-consuming its own module. Replaces the former
        # treefmt-nix `treefmtEval`. The bare default-backend binary (selfBin) is
        # used here — the formatter wrapper and check gate only need the
        # executable, and reusing the default backend avoids building the other
        # backend during lint. `package` is required because conformist is not
        # in nixpkgs.
        conformistEval = conformistLib.evalModule pkgs {
          imports = [ ./nix/conformist.nix ];
          package = selfBin;
        };

        # IMPURE self-check config: git-state whole-tree checks (e.g. git-remotes)
        # that need a live .git and so cannot run in the sandboxed
        # checks.formatting. `just check-worktree` builds this config and runs
        # `conformist check` against the working tree. See nix/conformist-impure.nix.
        conformistImpureEval = conformistLib.evalModule pkgs {
          imports = [ ./nix/conformist-impure.nix ];
          package = selfBin;
        };

        # Eval-only smoke test over the full program + linter registries:
        # checks.<sys>.{formatter-<name>,linter-<name>}. Forces module eval +
        # config generation for every ported tool, catching schema breakage
        # without building each tool. See nix/checks.nix.
        registryChecks = import ./nix/checks.nix {
          inherit pkgs;
          lib = conformistLib;
        };

        # Behavioral fixture tests for the whole-tree linters: run each compiled
        # check against pass/fail fixture trees and assert the exit code + an
        # output token. checks.<sys>.{linter-fixture-<name>-<label>, linter-fixtures
        # (aggregate)}. Built cheaply by `just verify-linter-fixtures`. See
        # nix/linter-fixtures.nix (conformist#17).
        linterFixtureChecks = import ./nix/linter-fixtures.nix {
          inherit pkgs;
          lib = conformistLib;
        };
      in
      {
        packages = {
          # Per-system default (godynSystem): godyn (native) build + man pages on
          # x86_64-linux, the bga build + man pages everywhere else (igloo#33).
          default = conformistDefault;
          conformist = conformistDefault;
          # bga (buildGoApplication) build + man pages: ca-derivations-free,
          # a single input-addressed derivation. The default on non-godyn
          # systems; opt-in on x86_64-linux.
          conformist-bga = conformist-bga;
          # The bare godyn binary for the fast edit loop and the backend bench
          # (`nix build .#conformist-native`, `.#conformist-native.passthru.bga`); no man
          # pages. See conformist-native above. Only builds on godynSystem until
          # igloo#33 lands (linux-generated graph).
          conformist-native = conformist-native;
          # The compiled man pages on their own, for inspection
          # (`nix build .#manpages`); built with the per-system default backend's
          # binary, and also bundled into the conformist package.
          manpages = if godynSystem then manpages else manpagesBga;
          # The generated config for the impure (git-state) self-checks, consumed
          # by `just check-worktree`.
          conformist-impure-config = conformistImpureEval.config.build.configFile;
          # The custom golangci-lint carrying dewey's analyzers, re-exported from
          # purse-first (#134) so `just lint-go` builds it via `.#golangci-lint-dewey`
          # (binary: bin/golangci-lint-dewey). conformist#10.
          golangci-lint-dewey = purse-first.packages.${system}.golangci-lint-dewey;
        };

        # `nix fmt` writes (repair mode); `checks.formatting` is the sandboxed
        # read-only `conformist check` gate built by `just lint-fmt`. The
        # `formatter-*` / `linter-*` checks are the registry smoke test.
        formatter = conformistEval.config.build.wrapper;
        checks =
          registryChecks
          // linterFixtureChecks
          // {
            formatting = conformistEval.config.build.check self;

            # Regression test for the sandbox-safe script-linter helper
            # (conformist#19). Packages an example `#!/usr/bin/env bash` script via
            # writeCheckScript and EXECUTES it inside the build sandbox — which has
            # no /usr/bin/env — so a missing patchShebangs would make exec fail here
            # (the very failure #19 describes), failing the build. This is the
            # dogfood proof that the helper produces sandbox-safe scripts.
            write-check-script =
              let
                example = conformistLib.writeCheckScript pkgs {
                  name = "example-check";
                  src = pkgs.writeText "example-check" "#!/usr/bin/env bash\necho ok\n";
                  runtimeInputs = [ pkgs.coreutils ];
                };
              in
              pkgs.runCommand "conformist-write-check-script-test" { } ''
                got=$(${example}/bin/example-check) || {
                  echo "write-check-script: example failed to exec in the pure sandbox (#19 regression)" >&2
                  exit 1
                }
                [ "$got" = "ok" ] || {
                  echo "write-check-script: unexpected output '$got'" >&2
                  exit 1
                }
                touch $out
              '';

            # True-positive regression for the eng-versioning deprecated-file rule
            # (conformist#14): run the linter's own command against fixtures and
            # assert it passes a clean tree but FLAGS a `version.txt` and a flake.nix
            # named version let-binding. checks.formatting only proves conformist's
            # own clean tree passes; this proves the rule actually fires.
            eng-versioning-deprecated-file =
              let
                cmd = conformistEval.config.settings.linter.eng-versioning-deprecated-file.command;
              in
              pkgs.runCommand "conformist-eng-versioning-deprecated-file-test" { } ''
                set -eu
                # Clean tree (flake.nix without a named version var, no version.txt) passes.
                mkdir -p clean
                printf '{ outputs = _: { }; }\n' > clean/flake.nix
                ( cd clean && ${cmd} ) || { echo "FAIL: clean tree was flagged" >&2; exit 1; }
                # version.txt at the repo root is flagged.
                mkdir -p vt
                printf '{ }\n' > vt/flake.nix
                printf '0.1.0\n' > vt/version.txt
                if ( cd vt && ${cmd} ); then echo "FAIL: version.txt not flagged" >&2; exit 1; fi
                # A named version let-binding in flake.nix is flagged. The semver is
                # passed as a printf arg so the matchable literal never appears in
                # *this* flake.nix source — otherwise the rule would (correctly) flag
                # conformist's own flake.nix.
                mkdir -p nv
                printf '{\n  fooVersion = "%s";\n}\n' 1.2.3 > nv/flake.nix
                if ( cd nv && ${cmd} ); then echo "FAIL: flake.nix named version var not flagged" >&2; exit 1; fi
                touch $out
              '';

            # True-positive regression for the git-remotes SSH-only rule
            # (conformist#8): spin up a throwaway repo and assert the linter passes
            # all-SSH remotes (scp-like + ssh://) but FLAGS http:// and git://.
            # lint-worktree only proves conformist's own SSH remotes pass; this
            # proves the non-SSH schemes actually fire.
            git-remotes =
              let
                cmd = conformistImpureEval.config.settings.linter.git-remotes.command;
              in
              pkgs.runCommand "conformist-git-remotes-test" { nativeBuildInputs = [ pkgs.git ]; } ''
                set -eu
                export HOME=$PWD
                git init -q repo
                cd repo
                # all-SSH remotes (scp-like and ssh://) pass.
                git remote add origin git@github.com:o/r.git
                git remote add up ssh://git@example.com/o/r.git
                ${cmd} || { echo "FAIL: all-SSH remotes were flagged" >&2; exit 1; }
                # an http:// remote is flagged.
                git remote add bad http://example.com/o/r.git
                if ${cmd}; then echo "FAIL: http:// remote not flagged" >&2; exit 1; fi
                git remote remove bad
                # a git:// remote is flagged.
                git remote add bad2 git://example.com/o/r.git
                if ${cmd}; then echo "FAIL: git:// remote not flagged" >&2; exit 1; fi
                touch $out
              '';

            # True-positive regression for the golangci-dewey wiring rule
            # (conformist#10): a golangci-gating repo with a .custom-gcl.yml that
            # references the dewey plugin passes; one without .custom-gcl.yml is
            # flagged; a repo that doesn't gate on golangci-lint is a no-op pass.
            golangci-dewey =
              let
                cmd = conformistEval.config.settings.linter.golangci-dewey.command;
              in
              pkgs.runCommand "conformist-golangci-dewey-test" { } ''
                set -eu
                # gates on golangci-lint + wires the dewey plugin -> passes.
                mkdir -p ok
                printf 'version: "2"\n' > ok/.golangci.yaml
                printf 'plugins:\n  - module: github.com/amarbel-llc/purse-first/libs/dewey\n' > ok/.custom-gcl.yml
                ( cd ok && ${cmd} ) || { echo "FAIL: wired repo was flagged" >&2; exit 1; }
                # gates on golangci-lint, no .custom-gcl.yml -> flagged.
                mkdir -p missing
                printf 'version: "2"\n' > missing/.golangci.yaml
                if ( cd missing && ${cmd} ); then echo "FAIL: missing .custom-gcl.yml not flagged" >&2; exit 1; fi
                # does not gate on golangci-lint -> no-op pass.
                mkdir -p none
                ( cd none && ${cmd} ) || { echo "FAIL: non-golangci repo was flagged" >&2; exit 1; }
                touch $out
              '';
          };

        devShells.default = pkgs-master.mkShell {
          packages = [
            # mkGoEnv puts the gomod2nix-regen `go` wrapper + the gomod2nix CLI
            # on PATH, so `just build-gomod2nix` / `just update-go` work.
            (pkgs.mkGoEnv { pwd = ./.; })
            # igloo's pkgs.go (1.26.3), matching conformistBin + the godyn
            # backend (igloo#29). godyn-gen runs `go list -deps -json` against
            # this go, so `just build-godyn-graph` regenerates the committed graph.
            pkgs.go
            pkgs.godyn-gen
            pkgs-master.gofumpt
            pkgs-master.golangci-lint
            pkgs-master.gopls
            pkgs.just
            # A real linter for dogfooding `conformist check` and for the
            # check/linter test paths (RFC 0001).
            pkgs.shellcheck
            # scdoc for ad-hoc local man-page preview; the authoritative build
            # is the `manpages` Nix derivation (eng-manpages(7) PRINCIPLE 4).
            pkgs.scdoc
          ]
          # Formatter binaries + test-fmt-* helpers the Go test suite shells
          # out to (cmd/root_test.go, format/formatter_test.go). Run via
          # `just test-go`, which evaluates this devShell fresh.
          ++ (import ./nix/packages/conformist/formatters.nix pkgs);
        };
      }
    ))
    // {
      # System-agnostic outputs.

      # The conformist Nix module library: evalModule / submoduleWith /
      # mkConfigFile / mkWrapper, plus the formatter (programs) and linter
      # registries. See nix/default.nix.
      lib = conformistLib;

      # flake-parts module: `perSystem.conformist`. See flake-module.nix.
      flakeModule = ./flake-module.nix;

      # `nix flake init -t github:amarbel-llc/conformist#eng` scaffolds a repo
      # already wired to conformist with the eng-convention preset: flake.nix
      # (conformist input + follows + evalModule), conformist.nix (imports
      # presets.eng + formatters), a conformist-justfile(7)-conformant justfile,
      # version.env, and .envrc. See templates/eng/.
      templates = {
        eng = {
          path = ./templates/eng;
          description = "amarbel-llc eng-conventions conformist setup (preset + recipes)";
        };
        default = self.templates.eng;
      };
    };
}
