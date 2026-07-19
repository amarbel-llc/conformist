{
  description = "conformist: the linter and formatter multiplexer";

  inputs = {
    # amarbel-llc/nixpkgs fork. Its overlay carries a patched
    # buildGoApplication that auto-injects `-X main.version` (read from
    # version.env in the module dir) and `-X main.commit` (from src.rev),
    # so no per-repo ldflags wiring is needed. See eng-versioning(7) and
    # amarbel-llc/nixpkgs#31.
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
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
    nixpkgs-master.url = "github:NixOS/nixpkgs/567a49d1913ce81ac6e9582e3553dd90a955875f";

    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";

    # NOTE: conformist deliberately does NOT take purse-first as a flake input.
    # conformist must be strictly UPSTREAM of purse-first (no cycle), but it
    # still dogfoods purse-first's dewey golangci-lint plugin on its own Go
    # (conformist#10). It consumes that as a fixed-output source fetch
    # (golangciLintDeweySrc in outputs, pinned by rev + hash) and builds the
    # golangci-lint-dewey binary itself — an FOD leaf that pins source by commit
    # and pulls NO flake graph, so purse-first can import conformist without
    # closing a loop. See conformist#45 coordination / the upstream-flip plan.
    utils.inputs.systems.follows = "igloo/systems";
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
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

        # Where the godyn (native) backend can build at all: only x86_64-linux
        # (godyn(7) LIMITATIONS). The committed godyn-graph.json is generated
        # there — `go list`'s file/import selection honors GOOS/GOARCH, so the
        # graph embeds linux/amd64-only sources (e.g. x/sys/unix's *_linux*.go +
        # asm_linux_amd64.s) that cannot compile on any other system (igloo#33).
        #
        # godyn is NO LONGER the default backend anywhere — bga (buildGoApplication
        # off gomod2nix.toml) is the default on every system, because the
        # single-platform graph and its hand-committed JSON are immature and the
        # JSON drifts from source (it broke the Linux build when cmd/conform gained
        # //go:embed patterns the graph didn't list). godyn stays opt-in via
        # `.#conformist-native`; this flag now only gates whether that opt-in
        # package is buildable on the current system. Re-promotion is a future
        # session, pending igloo#33 (per-system graphs) + dropping the JSON.
        godynSystem = system == "x86_64-linux";

        # The dewey golangci-lint plugin, consumed WITHOUT a flake input so
        # conformist stays upstream of purse-first (conformist#10 / the
        # upstream-flip plan). golangciLintDeweySrc is a fixed-output source
        # fetch — it pins purse-first by commit + hash and pulls no flake graph,
        # so there is no conformist->purse-first edge and purse-first may import
        # conformist without a cycle. Bump rev+hash deliberately to track the
        # plugin; re-prefetch with `nix-prefetch-github amarbel-llc purse-first
        # --rev <sha>`.
        golangciLintDeweySrc = pkgs.fetchFromGitHub {
          owner = "amarbel-llc";
          repo = "purse-first";
          rev = "00c193ed49b477fdf6c23450c35256c2251e3b72";
          hash = "sha256-5XL6TDTVUmVGj177DTtTUCDZoaLp/xGdv4b5oA/iM5c=";
        };

        # Build golangci-lint-dewey from the fetched source with the SAME recipe
        # purse-first uses (gomod.nix), minus the commit/date/version -X ldflags:
        # dropping them keeps this output reproducible across purse-first commits
        # (the upstream build stamps -X main.commit/date, which would churn the
        # hash). cmd/golangci-lint-dewey is a standalone module (own
        # gomod2nix.toml, GOWORK=off) whose only purse-first coupling is
        # `replace => ../../libs/dewey`, satisfied by the whole-repo fetch.
        golangciLintDeweyDir = "cmd/golangci-lint-dewey";
        golangciLintDewey = pkgs.buildGoApplication {
          pname = "golangci-lint-dewey";
          version = "dewey";
          go = pkgs.go;
          src = golangciLintDeweySrc;
          pwd = golangciLintDeweySrc + "/${golangciLintDeweyDir}";
          modRoot = golangciLintDeweyDir;
          modules = golangciLintDeweySrc + "/${golangciLintDeweyDir}/gomod2nix.toml";
          subPackages = [ "." ];
          GOWORK = "off";
          ldflags = [
            "-s"
            "-w"
          ];
          meta = {
            description = "golangci-lint with the dewey module plugin linked in (built from pinned purse-first source)";
            mainProgram = "golangci-lint-dewey";
          };
        };

        # bga (buildGoApplication) build — the ca-derivations-free backend behind
        # `.#conformist-bga` and the DEFAULT on every system (godynSystem above;
        # godyn is opt-in via `.#conformist-native`).
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

        # Man pages per backend: the bga default needs no ca-derivations; the
        # opt-in godyn package carries its own (manpages, built from conformist-native).
        manpages = mkManpages conformist-native;
        manpagesBga = mkManpages conformistBin;

        # The opt-in godyn package: the godyn (native) binary plus its man pages.
        # NOT the default — bga is (see conformistDefault below). Kept reachable
        # for the future godyn re-promotion work (igloo#33). `meta.mainProgram`
        # keeps `nix run` / `lib.getExe` resolving to bin/conformist.
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

        # The bga package: the single input-addressed buildGoApplication
        # derivation + bga-built man pages. ca-derivations-free and platform-
        # agnostic (no per-system graph), so it is the DEFAULT backend on every
        # system (see conformistDefault below) and what `.#conformist-bga` names
        # explicitly. See the backend bench (`just debug-bench-backends`) for the
        # godyn-vs-bga tradeoffs.
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

        # The default package and self-consumption binary: bga on every system.
        # godyn is opt-in (`.#conformist-native` / the `conformist` join), never
        # the default — see the godynSystem comment above.
        conformistDefault = conformist-bga;
        selfBin = conformistBin;

        # Opt-in native (godyn) build of the bare binary, driven by the committed
        # godyn-graph.json (igloo#29). buildGoAuto with strategy = "dev" selects
        # igloo's per-package godyn backend (`go tool compile`/`link` directly,
        # no `go build`). NO LONGER the default backend — bga is (conformistDefault
        # above); kept reachable via the `conformist` join (man pages) and
        # `.#conformist-native` (bare binary) for the fast inner loop, the backend
        # bench, and the future godyn re-promotion work. Its per-package outputs
        # are content-addressed, so building it requires the ca-derivations feature;
        # the input-addressed bga build is `.#conformist-bga`. Only builds on
        # x86_64-linux (godynSystem) until igloo#33 lands.
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
          # Default on every system: the bga (buildGoApplication) build + man
          # pages. Platform-agnostic, ca-derivations-free, no per-system graph.
          default = conformistDefault;
          conformist = conformistDefault;
          # The bga build + man pages, named explicitly. Same derivation as the
          # default above; kept as a stable name for the `conformist-bga` vs
          # `conformist-native` A/B (and the backend bench).
          conformist-bga = conformist-bga;
          # Opt-in: the bare godyn (native) binary for the fast edit loop and the
          # backend bench (`nix build .#conformist-native`,
          # `.#conformist-native.passthru.bga`); no man pages. NOT the default —
          # see conformist-native above. Only builds on godynSystem (x86_64-linux)
          # until igloo#33 lands (linux-generated graph).
          conformist-native = conformist-native;
          # The compiled man pages on their own, for inspection
          # (`nix build .#manpages`); built with the bga default backend's binary,
          # and also bundled into the conformist package.
          manpages = manpagesBga;
          # The generated config for the impure (git-state) self-checks, consumed
          # by `just check-worktree`.
          conformist-impure-config = conformistImpureEval.config.build.configFile;
          # conformist's own store-pinned pre-commit hook (issue #47):
          # `conformist --staged --exit-zero-on-fix` wrapped with the generated
          # config. Exposing it here dogfoods build.preCommit — `nix build
          # .#conformist-pre-commit` forces the new module output to build, and
          # it is on the devShell PATH as `conformist-pre-commit` for use as the
          # hook command.
          conformist-pre-commit = conformistEval.config.build.preCommit;
          # conformist's own store-pinned repair hook (conformist#54): the
          # `--commit --amend` sibling of conformist-pre-commit,
          # `conformist --commit --amend --exit-zero-on-fix` wrapped with the
          # generated config. Exposing it here dogfoods build.repair — `nix build
          # .#conformist-repair` forces the new module output to build, and it is
          # on the devShell PATH as `conformist-repair` for use as a spinclass
          # pre-merge repair hook (`repair = "conformist-repair"`).
          conformist-repair = conformistEval.config.build.repair;
          # The custom golangci-lint carrying dewey's analyzers, built locally
          # from pinned purse-first source (golangciLintDewey above) so `just
          # lint-go` builds it via `.#golangci-lint-dewey` (binary:
          # bin/golangci-lint-dewey). No longer re-exported from a purse-first
          # flake input — that edge was removed to keep conformist upstream of
          # purse-first (conformist#10 / upstream-flip).
          golangci-lint-dewey = golangciLintDewey;
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

            # Regression for the toolchain wrapper helper (conformist#51): wrap a
            # STUB conformist (which fails unless a stub formatter is on PATH)
            # with that formatter as a `tools` entry, then exec the wrapper with a
            # DELIBERATELY EMPTY ambient PATH. If the helper did not put `tools`
            # on PATH, the stub formatter would be unresolved and the wrapper
            # would fail here — so a green build proves the wrapper is
            # toolchain-hermetic (does not rely on the ambient environment) and
            # passes "$@" through to conformist.
            wrap-with-toolchain =
              let
                # Stub formatter the wrapped "conformist" requires on PATH.
                stubTool = pkgs.writeShellScriptBin "stub-formatter" "echo formatted";
                # Stub conformist: echoes its args (proves pass-through) and
                # invokes stub-formatter by bare name (proves tools are on PATH).
                stubConformist = pkgs.writeShellScriptBin "conformist" ''
                  stub-formatter >/dev/null
                  echo "conformist-args: $*"
                '';
                wrapper = conformistLib.wrapWithToolchain pkgs {
                  conformist = stubConformist;
                  tools = [ stubTool ];
                  name = "conformist-fmt";
                };
              in
              pkgs.runCommand "conformist-wrap-with-toolchain-test" { } ''
                # Empty ambient PATH: the only way stub-formatter resolves is via
                # the wrapper's own runtimeInputs.
                got=$(PATH= ${wrapper}/bin/conformist-fmt --staged --exit-zero-on-fix) || {
                  echo "wrap-with-toolchain: wrapper failed to exec with empty PATH — toolchain not hermetic (#51)" >&2
                  exit 1
                }
                [ "$got" = "conformist-args: --staged --exit-zero-on-fix" ] || {
                  echo "wrap-with-toolchain: args not passed through; got '$got'" >&2
                  exit 1
                }
                touch $out
              '';

            # Regression for mkToolchainHooks (conformist#59): the TOML-consumer
            # mirror of build.{wrapper,preCommit,repair}. Wrap a STUB conformist
            # (which fails unless a stub formatter is on PATH) and assert each of
            # the three returned wrappers (a) execs hermetically under an EMPTY
            # ambient PATH — proving `tools` is on PATH — and (b) bakes the right
            # mode flags plus the subdir-robust --tree-root-file. A green build
            # proves the helper produces three correctly-shaped, toolchain-hermetic
            # wrappers that pass "$@" through.
            mk-toolchain-hooks =
              let
                stubTool = pkgs.writeShellScriptBin "stub-formatter" "echo formatted";
                stubConformist = pkgs.writeShellScriptBin "conformist" ''
                  stub-formatter >/dev/null
                  echo "conformist-args: $*"
                '';
                hooks = conformistLib.mkToolchainHooks pkgs {
                  conformist = stubConformist;
                  tools = [ stubTool ];
                };
              in
              pkgs.runCommand "conformist-mk-toolchain-hooks-test" { } ''
                # Each wrapper is exec'd with an EMPTY ambient PATH: stub-formatter
                # resolves only via the wrapper's own runtimeInputs (hermetic).
                check() {
                  local bin="$1" want="$2" got
                  got=$(PATH= "$bin") || {
                    echo "mk-toolchain-hooks: $bin failed under empty PATH — not hermetic (#59)" >&2
                    exit 1
                  }
                  [ "$got" = "$want" ] || {
                    echo "mk-toolchain-hooks: $bin args wrong; got '$got' want '$want'" >&2
                    exit 1
                  }
                }
                check ${hooks.formatter}/bin/conformist \
                  "conformist-args: --tree-root-file=flake.nix"
                check ${hooks.preCommit}/bin/conformist-pre-commit \
                  "conformist-args: --tree-root-file=flake.nix --staged --exit-zero-on-fix"
                check ${hooks.repair}/bin/conformist-repair \
                  "conformist-args: --tree-root-file=flake.nix --commit --amend --exit-zero-on-fix"
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

            # True-positive regression for the git-remotes SSH-only + canonical-host
            # rule (conformist#8): spin up a throwaway repo and assert the linter
            # passes all-SSH remotes on approved hosts (scp-like + ssh://) but FLAGS
            # http:// and git://. lint-worktree only proves conformist's own SSH
            # remotes pass; this proves the non-SSH schemes actually fire.
            # `origin` is on code.linenisgreat.com (the default canonical-host) —
            # NOT github.com — since the host-canonicality rule added alongside the
            # forge migration checks `origin`'s host specifically; a github.com
            # origin would (correctly) now fail that separate rule and defeat this
            # test's transport-only intent.
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
                git remote add origin git@code.linenisgreat.com:o/r.git
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
            # igloo's pkgs.go (1.26.3), matching conformistBin + the opt-in godyn
            # backend (igloo#29). godyn-gen runs `go list -deps -json` against
            # this go, so `just debug-godyn-graph` regenerates the committed graph.
            pkgs.go
            pkgs.godyn-gen
            pkgs-master.gofumpt
            pkgs-master.golangci-lint
            pkgs-master.gopls
            pkgs.just
            # conformist's own config-specific, toolchain-hermetic hook wrappers
            # (build.preCommit / build.repair), on PATH as `conformist-pre-commit`
            # / `conformist-repair` so the sweatfile can name them — the same
            # self-consumption templates/eng prescribes to adopters (#47/#54/#59).
            conformistEval.config.build.preCommit
            conformistEval.config.build.repair
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

      # `nix flake init -t 'git+https://code.linenisgreat.com/conformist.git#eng'`
      # scaffolds a repo
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
