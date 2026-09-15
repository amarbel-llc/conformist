{
  description = "conformist: the linter and formatter multiplexer";

  inputs = {
    # amarbel-llc/nixpkgs fork: the source of conformist's Go build. godyn
    # (buildGoAuto / buildGodynModule) builds from the go.nix manifest (igloo
    # FDR 0008) with igloo's registry toolchain (pkgs.goToolchain.go, FDR 0012),
    # auto-injecting `-X main.version` (version.env) and `-X main.commit`
    # (src.rev). See eng-versioning(7) and godyn(7).
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
    # Apply igloo's overlay to OUR pinned nixpkgs-master (the hydra-vetted
    # sha eng pins) instead of igloo's own committed copy of the same pin.
    # Only igloo's flake-path outputs honor this — `pkgs` below therefore
    # uses `igloo.legacyPackages`, not the `import igloo {}` shim, which
    # reads igloo's committed flake.lock and is follows-immune (igloo#37).
    igloo.inputs.nixpkgs-master.follows = "nixpkgs-master";

    # Pinned plain nixpkgs, source of the devShell's gofumpt.
    nixpkgs-master.url = "github:NixOS/nixpkgs/f13ff45afd1bb73e640eaa08a7066dbed07e3238";

    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";

    # NOTE: conformist deliberately does NOT take purse-first as a flake input.
    # conformist must be strictly UPSTREAM of purse-first (no cycle), but it
    # still runs purse-first's dewey analyzers on its own Go (conformist#10). It
    # builds them from a fixed-output source fetch (deweySrc in outputs, pinned
    # by rev + hash) — an FOD leaf that pins source by commit and pulls NO flake
    # graph, so purse-first can import conformist without closing a loop.
    utils.inputs.systems.follows = "igloo/systems";
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
    }@inputs:
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

        # The dewey analyzers, built WITHOUT a flake input so conformist stays
        # upstream of purse-first (conformist#10). deweySrc is a fixed-output
        # source fetch — it pins purse-first by commit + hash and pulls no flake
        # graph. Bump rev+hash deliberately to track the analyzers; re-prefetch
        # with `nix-prefetch-github amarbel-llc purse-first --rev <sha>`.
        deweySrc = pkgs.fetchFromGitHub {
          owner = "amarbel-llc";
          repo = "purse-first";
          rev = "20f89c28ebf95f9ed6bab2f13101d7af8d27cb03";
          hash = "sha256-rCmYppRS2JEBbAXFnHg54I9ecY9AO94ruFgdUvdYd7U=";
        };

        # One dewey analyzer (libs/dewey/cmd/<name>, a unitchecker) as a
        # standalone binary for godyn's vet lane (vetTool). purse-first is a
        # go.work workspace with one root gomod2nix.toml; the analyzer builds
        # from the libs/dewey module against that lock with the workspace off.
        mkDeweyAnalyzer =
          name:
          pkgs.buildGoApplication {
            pname = name;
            version = "dewey";
            src = deweySrc;
            pwd = deweySrc + "/libs/dewey";
            modRoot = "libs/dewey";
            modules = deweySrc + "/gomod2nix.toml";
            subPackages = [ "cmd/${name}" ];
            GOWORK = "off";
            ldflags = [
              "-s"
              "-w"
            ];
            meta.mainProgram = name;
          };

        # The dewey analyzers conformist gates on, each as its own vet lane.
        deweyAnalyzerNames = [
          "defererr"
          "repool"
          "seqerror"
          "testui"
        ];

        # The shared shape of every conformist Go build: godyn over the go.nix
        # manifest (igloo FDR 0008). go.mod, gomod2nix.toml and the package graph
        # are rendered or derived inside nix — nothing is committed, so none of
        # them can drift. version + commit are injected automatically.
        godynModuleArgs = {
          pname = "conformist";
          src = self;
          manifest = ./go.nix;
          inherit inputs system;
        };

        # Burn in git by store path so the tree walker, tree-root detection and
        # the --commit/--staged lanes never depend on the caller's PATH having
        # git (RFC 0005 profile route: a bare pre-merge hook, a jq/git-less
        # host). Puts git in conformist's runtime closure.
        gitBinaryLdflags = [ "-X code.linenisgreat.com/conformist/git.Binary=${pkgs.git}/bin/git" ];

        # conformist and flakeclobber (RFC 0004, the fleet-migration sweeper —
        # built here so it cannot rot unbuilt; a SEPARATE one-shot binary, never
        # wired into `conform`). buildGoAuto with no `strategy` picks godyn on
        # every system igloo supports (godyn(7) buildGoAuto) and keeps
        # buildGoApplication reachable as passthru.bga (`.#conformist-bga`, the
        # ca-derivations-free escape hatch). godyn's per-package outputs are
        # content-addressed, so building conformist needs the ca-derivations
        # feature. Integration tests run in their own tests instance below.
        conformistBin = pkgs.buildGoAuto {
          inherit (godynModuleArgs)
            pname
            src
            manifest
            inputs
            ;
          subPackages = [
            "."
            "cmd/flakeclobber"
          ];
          ldflags = gitBinaryLdflags;
          bgaArgs = {
            GOTOOLCHAIN = "local";
            doCheck = false;
          };
        };

        # godyn's per-package go test lane (checks.conformist-tests). The test
        # graph is derived at eval time and each tested package's run is its
        # own content-addressed derivation, so an unchanged test cone never
        # re-runs. `tags = [ "test" ]`: dewey's test_ui package sits behind a
        # `test` build constraint, so the tests build from this second instance
        # (untouched packages are shared, CA). The runs shell out to real
        # formatters, git, jj and gofmt; testFiles places the module files the
        # tests read from outside their package (test/examples, test/config,
        # templates/eng, conformist.profile).
        conformistTests = pkgs.buildGodynModule (
          godynModuleArgs
          // {
            tags = [ "test" ];
            tests = true;
            nativeCheckInputs = [
              # The tests write helper scripts and resolve bash by absolute path
              # (the sandbox has no /usr/bin/env).
              pkgs.bash
              pkgs.git
              pkgs.goToolchain.go
              pkgs.just
              pkgs.shellcheck
            ]
            ++ (import ./nix/packages/conformist/formatters.nix pkgs);
            testPreRun = ''
              export HOME="$TMPDIR"
            '';
            testFiles = {
              "cmd" = [
                "test/examples"
                "test/config"
              ];
              "cmd/conform" = [ "templates/eng" ];
              "config" = [ "test/examples" ];
              "profile" = [ "conformist.profile" ];
              "walk" = [
                "test/examples"
                "test/config"
              ];
            };
          }
        );

        # One dewey analyzer as godyn's per-package vet lane with that analyzer
        # as vetTool. The instances share every compile (content-addressed), so
        # the added cost is the vet runs. `tags = [ "test" ]` like the tests
        # instance: test/test.go uses dewey's test_ui, which only exists under
        # that tag, so an untagged instance fails to type-check it.
        deweyAnalyzerVet =
          name:
          (pkgs.buildGodynModule (
            godynModuleArgs
            // {
              tags = [ "test" ];
              vetTool = mkDeweyAnalyzer name;
            }
          )).passthru.vetAll;

        # Man pages, built by Nix per eng-manpages(7) PRINCIPLE 4 (not a justfile
        # recipe, not CI): scdoc compiles the hand-written section-5/7 sources in
        # doc/, and `conformist gen-man` renders the section-1 CLI reference from
        # the cobra command tree (PRINCIPLE 3). This derivation IS the man-page
        # lint — a malformed .scd fails the build. Rendered roff is never committed.
        # Parameterised by the conformist binary used to run `gen-man`, so each
        # backend's package bundles man pages built with its own binary.
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

        manpages = mkManpages conformistBin;

        # The default package: the godyn binaries plus their man pages.
        # `meta.mainProgram` keeps `nix run` / `lib.getExe` resolving to
        # bin/conformist.
        conformist = pkgs.symlinkJoin {
          name = "conformist";
          paths = [
            conformistBin
            manpages
          ];
          meta = (conformistBin.meta or { }) // {
            mainProgram = "conformist";
          };
          # godyn-go (`just update-go`) builds packages.<system>.default and needs
          # goRun / ingest from the manifest build.
          inherit (conformistBin) passthru;
        };

        # The buildGoApplication escape hatch from the godyn default: the same
        # go.nix, input-addressed, no ca-derivations. Also the bga side of the
        # backend bench (`just debug-bench-backends`).
        conformist-bga = pkgs.symlinkJoin {
          name = "conformist-bga";
          paths = [
            conformistBin.passthru.bga
            (mkManpages conformistBin.passthru.bga)
          ];
          meta = (conformistBin.passthru.bga.meta or { }) // {
            mainProgram = "conformist";
          };
        };

        # conformist as a portable static release binary, published as a forge
        # release asset by `just deploy-release-assets` (the release-assets
        # post-merge target). buildGoApplication with cgo off (godyn's shared
        # stdlib is cgo-on, so godyn cannot link a static net-importing binary
        # yet), pure-Go net/user, stripped, no git store path, and
        # allowedReferences = [] so the build fails if any /nix/store path
        # survives in the binary. x86_64-linux only for now: the aarch64 cross
        # build links externally with the host gcc even with cgo off.
        conformist-static = pkgs.buildGoAuto {
          inherit (godynModuleArgs)
            pname
            src
            manifest
            inputs
            ;
          strategy = "bga";
          subPackages = [ "." ];
          tags = [
            "netgo"
            "osusergo"
          ];
          ldflags = [
            "-s"
            "-w"
          ];
          # nixpkgs' Go patches embed three data paths. tzdata and mailcap are
          # prepended to Go's search lists, so stripping them falls back to
          # /usr/share/zoneinfo and /etc/mime.types. iana-etc REPLACES
          # /etc/services and /etc/protocols; stripped, those lookups find no
          # file and fall back to net's built-in tables (the https profile
          # check passes on the stripped x86_64 binary).
          nativeBuildInputs = [ pkgs.removeReferencesTo ];
          postInstall = ''
            find "$out/bin" -type f -exec remove-references-to \
              -t ${pkgs.tzdata} \
              -t ${pkgs.mailcap} \
              -t ${pkgs.iana-etc} \
              {} +
          '';
          bgaArgs = {
            GOTOOLCHAIN = "local";
            CGO_ENABLED = "0";
            doCheck = false;
            allowedReferences = [ ];
          };
        };

        # conformist self-consuming its own module. Replaces the former
        # treefmt-nix `treefmtEval`. The bare default binary is used here — the
        # formatter wrapper and check gate only need the executable. `package`
        # is required because conformist is not in nixpkgs.
        conformistEval = conformistLib.evalModule pkgs {
          imports = [ ./nix/conformist.nix ];
          package = conformistBin;
        };

        # IMPURE self-check config: git-state whole-tree checks (e.g. git-remotes)
        # that need a live .git and so cannot run in the sandboxed
        # checks.formatting. `just lint-worktree` builds this config and runs
        # `conformist check` against the working tree. See nix/conformist-impure.nix.
        conformistImpureEval = conformistLib.evalModule pkgs {
          imports = [ ./nix/conformist-impure.nix ];
          package = conformistBin;
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

        # The git merge drivers for generated files (conformist-git(7) MERGE
        # DRIVERS). Not linter commands — git invokes these directly once the
        # `merge.<name>.driver` registration puts them on PATH; the
        # `git-merge-drivers` linter only checks the .gitattributes half.
        mergeDrivers = import ./nix/merge-drivers.nix { inherit pkgs; };
      in
      {
        packages = {
          # Default on every system: the godyn build + man pages.
          default = conformist;
          inherit
            conformist
            conformist-bga
            conformist-static
            manpages
            ;
          # godyn's tests instance (tests = true, tags = [ "test" ]), exposed for
          # its passthru.testWith: `just debug-test-pkg` (godyn-test -A).
          conformist-godyn-tests = conformistTests;
          # conformist's own generated conformist.toml for the PURE lane, consumed
          # by `just explore-show-config`. Exposed so that recipe inspects the
          # config conformist ACTUALLY uses, rather than re-deriving one by
          # evaluating ./nix/conformist.nix standalone. Those two silently diverge
          # the moment flake.nix adds anything to conformistEval, and a diagnostic
          # that confidently reports a config the tool does not use is worse than
          # no diagnostic at all — that happened once already, during the just-us
          # linter move.
          conformist-config = conformistEval.config.build.configFile;
          # The generated config for the impure (git-state) self-checks, consumed
          # by `just lint-worktree`.
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
          # The git merge drivers for generated files (conformist-git(7) MERGE
          # DRIVERS). Exposed as packages because they are installed onto PATH
          # fleet-wide and registered once in git config — they are invoked by
          # git, not by conformist. `nix build .#conformist-merge-flake-lock`
          # also forces the writeShellApplication to build, which is where
          # shellcheck runs on the generated shell.
          conformist-merge-flake-lock = mergeDrivers.flake-lock;
          conformist-merge-codegen-header = mergeDrivers.codegen-header;
        };

        # `nix fmt` writes (repair mode); `checks.formatting` is the sandboxed
        # read-only `conformist check` gate built by `just lint-fmt`. The
        # `formatter-*` / `linter-*` checks are the registry smoke test.
        formatter = conformistEval.config.build.wrapper;
        checks =
          registryChecks
          // linterFixtureChecks
          // builtins.listToAttrs (
            map (name: {
              name = "dewey-${name}";
              value = deweyAnalyzerVet name;
            }) deweyAnalyzerNames
          )
          // {
            formatting = conformistEval.config.build.check self;

            # godyn's per-package lanes from go.nix: the go test runs (`just
            # test-go`), the toolchain's go vet and godyn-lint (vet passes +
            # staticcheck defaults, //nolint honored) (`just lint-go`). The
            # dewey-<name> vet lanes above are `just lint-go-analyzers`.
            conformist-tests = conformistTests.passthru.checkAll;
            vet = conformistTests.passthru.vetAll;
            lint = conformistTests.passthru.lintAll;

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
                printf 'plugins:\n  - module: code.linenisgreat.com/purse-first/libs/dewey\n' > ok/.custom-gcl.yml
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
            # No ambient `go`, gopls or gomod2nix: dependencies live in go.nix
            # (igloo FDR 0008). go commands run through `godyn-go -- <cmd>`
            # (inside nix, results patched back), one package's tests through
            # `godyn-test <dir> -- <flags>`.
            pkgs.godyn-go
            pkgs.godyn-test
            pkgs-master.gofumpt
            pkgs.just
            # conformist's own config-specific, toolchain-hermetic hook wrappers
            # (build.preCommit / build.repair), on PATH as `conformist-pre-commit`
            # / `conformist-repair` so the sweatfile can name them — the same
            # self-consumption templates/eng prescribes to adopters (#47/#54/#59).
            conformistEval.config.build.preCommit
            conformistEval.config.build.repair
            # A real linter for dogfooding `conformist check` (RFC 0001).
            pkgs.shellcheck
            # scdoc for ad-hoc local man-page preview; the authoritative build
            # is the `manpages` Nix derivation (eng-manpages(7) PRINCIPLE 4).
            pkgs.scdoc
          ];
        };
      }
    ))
    // {
      # System-agnostic outputs.

      # The conformist Nix module library: evalModule / submoduleWith /
      # mkConfigFile / mkWrapper, plus the formatter (programs) and linter
      # registries. See nix/default.nix. `lib.profile` is the canonical conformist
      # profile (RFC 0005) as a store path: consumers read
      # `${inputs.conformist.lib.profile}` instead of keeping a copy that drifts
      # (circus signs and serves it at /papi/conformist-profile). A plain path, so
      # reading it evaluates none of conformist's Go build.
      lib = conformistLib // {
        profile = ./conformist.profile;
      };

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
