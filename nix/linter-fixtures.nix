# Behavioral fixture tests for the whole-tree linters in nix/linters/.
#
# nix/checks.nix proves each linter module *evaluates*; this proves each linter
# *behaves* — it runs the compiled check against a crafted fixture tree and
# asserts the exit code (and optionally an output token). It closes the gap where
# a linter's failure path / language variant was only ever verified by hand
# against conformist's own tree (conformist#17 "checks fixture"; the #29 Cargo
# path and #23 undocumented-debug-recipe rejection had no automated home).
#
# How it gets the binary: `lib.evalModule pkgs { linters.<name>.enable = true; }`
# exposes `config.settings.linter.<name>.command` — the store path to the check
# (same access nix/checks.nix uses). Each linter is a PATH-wrapped
# writeShellApplication, so the fixture derivation just RUNS the command; it does
# not need to provide just/jq/etc. itself.
#
# Usage (see flake.nix): import ./nix/linter-fixtures.nix { inherit pkgs; lib = conformistLib; }
# Returns an attrset of individual `linter-fixture-<name>-<label>` checks plus an
# aggregate `linter-fixtures` (a link farm forcing them all to build).
{
  pkgs,
  lib, # conformist library (evalModule); nixpkgs lib is pkgs.lib below
}:
let
  nixlib = pkgs.lib;

  # Materialize an attrset of project-relative-path -> content into the cwd,
  # via the store so no shell heredoc escaping is needed. Read-only (444) is
  # fine — the linters only read fixtures.
  writeFixtureFiles =
    files:
    nixlib.concatStringsSep "\n" (
      nixlib.mapAttrsToList (
        path: content:
        let
          f = pkgs.writeText "fixture-file" content;
        in
        ''
          mkdir -p "$(dirname ${nixlib.escapeShellArg path})"
          cp ${f} ${nixlib.escapeShellArg path}
        ''
      ) files
    );

  # name        : linter name (key under linters.<name> and settings.linter.<name>)
  # label       : fixture label (becomes the derivation suffix)
  # enableModule: extra options merged into `linters.<name>` (e.g. { key = ...; })
  # files       : attrset of relpath -> content written into the fixture tree
  # expectFail  : true => the linter MUST exit non-zero; false => MUST exit zero
  # expectToken : optional substring the linter output MUST contain
  mkLinterFixtureCheck =
    {
      name,
      label,
      enableModule ? { },
      files,
      expectFail ? false,
      expectToken ? null,
    }:
    let
      mod = lib.evalModule pkgs {
        enableDefaultExcludes = false;
        linters.${name} = {
          enable = true;
        }
        // enableModule;
      };
      cmd = mod.config.settings.linter.${name}.command;

      assertExit =
        if expectFail then
          ''
            if [ "$rc" -eq 0 ]; then
              echo "FIXTURE FAIL: expected linter '${name}' to reject ${label}, but it exited 0" >&2
              exit 1
            fi
          ''
        else
          ''
            if [ "$rc" -ne 0 ]; then
              echo "FIXTURE FAIL: expected linter '${name}' to pass ${label}, but it exited $rc" >&2
              exit 1
            fi
          '';

      assertToken = nixlib.optionalString (expectToken != null) ''
        if ! grep -qF ${nixlib.escapeShellArg expectToken} out.log; then
          echo "FIXTURE FAIL: linter '${name}' output did not contain ${nixlib.escapeShellArg expectToken}" >&2
          exit 1
        fi
      '';
    in
    pkgs.runCommandLocal "linter-fixture-${name}-${label}" { } ''
      mkdir fixture && cd fixture
      ${writeFixtureFiles files}

      # The whole-tree check runs at the tree root (cwd) with no file arguments.
      if ${cmd} >out.log 2>&1; then rc=0; else rc=$?; fi
      cat out.log

      ${assertExit}
      ${assertToken}

      touch $out
    '';

  # ---- git-remotes: a live-git linter (check reads remotes, repair mutates) ----
  #
  # Unlike the pure whole-tree checks above, git-remotes reads live git state
  # (`git remote -v`) and its repair rewrites it (`git remote set-url`). The
  # fixture therefore `git init`s a throwaway repo, `git remote add`s the given
  # URLs, then runs the check or the repair and asserts. git init / remote ops
  # are offline, so this still runs in the pure runCommandLocal sandbox
  # (conformist#68).
  gitRemotesMod = lib.evalModule pkgs {
    enableDefaultExcludes = false;
    linters.git-remotes.enable = true;
  };
  gitRemotesCheck = gitRemotesMod.config.settings.linter.git-remotes.command;
  gitRemotesRepair = gitRemotesMod.config.settings.linter.git-remotes."repair-command";

  # label        : fixture label (derivation suffix)
  # remotes      : attrset remote-name -> URL to `git remote add`
  # action       : "check" (run the read-only command) or "repair" (run repair)
  # expectFail   : check action only — true => check MUST exit non-zero
  # expectToken  : optional substring the action output MUST contain
  # expectRemotes: repair action only — attrset remote-name -> expected fetch URL
  #                after repair (asserted via `git remote get-url`)
  # recheckPasses: repair action only — whether the read-only check MUST pass
  #                after the repair (false when a non-github remote stays flagged)
  mkGitRemotesFixture =
    {
      label,
      remotes,
      action ? "check",
      expectFail ? false,
      expectToken ? null,
      expectRemotes ? { },
      recheckPasses ? true,
    }:
    let
      addRemotes = nixlib.concatStringsSep "\n" (
        nixlib.mapAttrsToList (
          name: url: "git remote add ${nixlib.escapeShellArg name} ${nixlib.escapeShellArg url}"
        ) remotes
      );

      assertToken = nixlib.optionalString (expectToken != null) ''
        if ! grep -qF ${nixlib.escapeShellArg expectToken} out.log; then
          echo "FIXTURE FAIL: git-remotes ${label} output did not contain ${nixlib.escapeShellArg expectToken}" >&2
          exit 1
        fi
      '';

      checkBody = ''
        if ${gitRemotesCheck} >out.log 2>&1; then rc=0; else rc=$?; fi
        cat out.log
        ${
          if expectFail then
            ''
              if [ "$rc" -eq 0 ]; then
                echo "FIXTURE FAIL: expected git-remotes to reject ${label}, but it exited 0" >&2
                exit 1
              fi
            ''
          else
            ''
              if [ "$rc" -ne 0 ]; then
                echo "FIXTURE FAIL: expected git-remotes to pass ${label}, but it exited $rc" >&2
                exit 1
              fi
            ''
        }
        ${assertToken}
      '';

      assertRemotes = nixlib.concatStringsSep "\n" (
        nixlib.mapAttrsToList (name: url: ''
          actual=$(git remote get-url ${nixlib.escapeShellArg name})
          expected=${nixlib.escapeShellArg url}
          if [ "$actual" != "$expected" ]; then
            echo "FIXTURE FAIL: git-remotes repair ${label}: remote '${name}' is '$actual', expected '$expected'" >&2
            exit 1
          fi
        '') expectRemotes
      );

      repairBody = ''
        # Repair must succeed; a second pass proves idempotency (both exit 0
        # under the sandbox's set -e).
        ${gitRemotesRepair} >out.log 2>&1
        cat out.log
        ${gitRemotesRepair} >/dev/null 2>&1
        ${assertToken}
        ${assertRemotes}
        # The read-only check must (recheckPasses) / must not pass after repair.
        if ${gitRemotesCheck} >check.log 2>&1; then crc=0; else crc=$?; fi
        cat check.log
        ${
          if recheckPasses then
            ''
              if [ "$crc" -ne 0 ]; then
                echo "FIXTURE FAIL: git-remotes ${label}: check still failed after repair (exit $crc)" >&2
                exit 1
              fi
            ''
          else
            ''
              if [ "$crc" -eq 0 ]; then
                echo "FIXTURE FAIL: git-remotes ${label}: check unexpectedly passed after repair (a non-github remote should stay flagged)" >&2
                exit 1
              fi
            ''
        }
      '';
    in
    pkgs.runCommandLocal "linter-fixture-git-remotes-${label}"
      {
        nativeBuildInputs = [ pkgs.git ];
      }
      ''
        export HOME="$PWD/home"
        mkdir -p "$HOME"
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
        mkdir fixture && cd fixture
        git init -q
        ${addRemotes}

        ${if action == "repair" then repairBody else checkBody}

        touch $out
      '';

  gitRemotesFixtures = [
    # Check: a github https remote is flagged; an SSH remote passes.
    (mkGitRemotesFixture {
      label = "check-https-fail";
      remotes.origin = "https://github.com/amarbel-llc/hyphence.git";
      expectFail = true;
      expectToken = "non-SSH remote";
    })
    (mkGitRemotesFixture {
      label = "check-ssh-pass";
      remotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })

    # Repair: each github network transport rewrites to the SSH form, the recheck
    # then passes, and a missing `.git` suffix is normalized in.
    (mkGitRemotesFixture {
      label = "repair-https";
      action = "repair";
      remotes.origin = "https://github.com/amarbel-llc/hyphence.git";
      expectToken = "rewrote remote 'origin'";
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })
    (mkGitRemotesFixture {
      label = "repair-https-no-dotgit";
      action = "repair";
      remotes.origin = "https://github.com/amarbel-llc/hyphence";
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })
    (mkGitRemotesFixture {
      label = "repair-http";
      action = "repair";
      remotes.origin = "http://github.com/amarbel-llc/hyphence.git";
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })
    (mkGitRemotesFixture {
      label = "repair-git-protocol";
      action = "repair";
      remotes.origin = "git://github.com/amarbel-llc/hyphence.git";
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })

    # Repair idempotency: an already-SSH remote is a no-op.
    (mkGitRemotesFixture {
      label = "repair-ssh-noop";
      action = "repair";
      remotes.origin = "git@github.com:amarbel-llc/hyphence.git";
      expectToken = "no github.com non-SSH remotes to rewrite";
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })

    # Repair selectivity: a non-github host has no canonical SSH form, so repair
    # leaves it alone and the read-only check keeps reporting it.
    (mkGitRemotesFixture {
      label = "repair-leaves-non-github";
      action = "repair";
      remotes.origin = "https://gitlab.com/amarbel-llc/hyphence.git";
      expectToken = "no github.com non-SSH remotes to rewrite";
      expectRemotes.origin = "https://gitlab.com/amarbel-llc/hyphence.git";
      recheckPasses = false;
    })
    (mkGitRemotesFixture {
      label = "repair-mixed";
      action = "repair";
      remotes = {
        origin = "https://github.com/amarbel-llc/hyphence.git";
        upstream = "https://gitlab.com/up/stream.git";
      };
      expectRemotes = {
        origin = "git@github.com:amarbel-llc/hyphence.git";
        upstream = "https://gitlab.com/up/stream.git";
      };
      recheckPasses = false;
    })
  ];

  # ---- clippy: a live-toolchain Rust linter (compiles the crate) ----------
  #
  # clippy COMPILES the crate, so its fixture needs the Rust toolchain and is
  # kept OUT of the `linter-fixtures` aggregate (the merge-hook lane) — it lives
  # in its own `clippy-fixtures` aggregate built on demand by
  # `just explore-clippy-fixture`, so the Rust toolchain never enters
  # conformist's CI. Interpolating the check/repair store paths forces the
  # writeShellApplications to build (so shellcheck runs on the generated shell).
  #
  # The crate is a LIB with `all-targets = false`, so clippy stays metadata-only
  # (no system linker needed in the bare runCommandLocal sandbox), and has no
  # dependencies, so cargo never reaches the registry (CARGO_NET_OFFLINE=1).
  clippyMod = lib.evalModule pkgs {
    enableDefaultExcludes = false;
    linters.clippy = {
      enable = true;
      all-targets = false;
    };
  };
  clippyCheck = clippyMod.config.settings.linter.clippy.command;
  clippyRepair = clippyMod.config.settings.linter.clippy."repair-command";

  clippyCargoToml = ''
    [package]
    name = "fixture-crate"
    version = "0.0.0"
    edition = "2021"

    [lib]
    path = "src/lib.rs"
  '';
  # `return 42;` trips clippy::needless_return (machine-applicable, so --fix
  # rewrites it to `42`); the clean form has no clippy findings.
  clippyDirtyLib = ''
    pub fn answer() -> i32 {
        return 42;
    }
  '';
  clippyCleanLib = ''
    pub fn answer() -> i32 {
        42
    }
  '';

  # label     : fixture label (derivation suffix)
  # libRs     : contents of src/lib.rs
  # action    : "check" (run the read-only command) or "repair" (run --fix)
  # expectFail: check action only — true => check MUST exit non-zero
  mkClippyFixture =
    {
      label,
      libRs,
      action ? "check",
      expectFail ? false,
    }:
    let
      runCheck = ''
        if ${clippyCheck} >out.log 2>&1; then rc=0; else rc=$?; fi
        cat out.log
        ${
          if expectFail then
            ''
              if [ "$rc" -eq 0 ]; then
                echo "FIXTURE FAIL: expected clippy to reject ${label}, but it exited 0" >&2
                exit 1
              fi
            ''
          else
            ''
              if [ "$rc" -ne 0 ]; then
                echo "FIXTURE FAIL: expected clippy to pass ${label}, but it exited $rc" >&2
                exit 1
              fi
            ''
        }
      '';
      runRepair = ''
        # Repair must succeed and remove the machine-applicable finding.
        ${clippyRepair} >out.log 2>&1
        cat out.log
        if grep -q 'return' src/lib.rs; then
          echo "FIXTURE FAIL: clippy repair ${label} did not remove the needless return" >&2
          cat src/lib.rs >&2
          exit 1
        fi
        # The read-only check must pass after the repair.
        if ${clippyCheck} >check.log 2>&1; then crc=0; else crc=$?; fi
        cat check.log
        if [ "$crc" -ne 0 ]; then
          echo "FIXTURE FAIL: clippy ${label}: check still failed after repair (exit $crc)" >&2
          exit 1
        fi
      '';
    in
    pkgs.runCommandLocal "clippy-fixture-${label}"
      {
        nativeBuildInputs = [ pkgs.git ];
      }
      ''
        export HOME="$PWD/home"
        mkdir -p "$HOME"
        export CARGO_HOME="$PWD/cargo-home"
        export CARGO_TARGET_DIR="$PWD/target"
        export CARGO_NET_OFFLINE=1
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
        mkdir crate && cd crate
        cp ${pkgs.writeText "Cargo.toml" clippyCargoToml} Cargo.toml
        mkdir src
        cp ${pkgs.writeText "lib.rs" libRs} src/lib.rs
        # Store-copied files are read-only (0444); `cargo --fix` must rewrite
        # src/lib.rs, so make the tree writable.
        chmod -R u+w .
        # `cargo --fix` requires a VCS (or --allow-no-vcs); init one and stage so
        # the repair's --allow-dirty/--allow-staged apply.
        git init -q
        git add -A

        ${if action == "repair" then runRepair else runCheck}

        touch $out
      '';

  clippyFixtures = [
    (mkClippyFixture {
      label = "check-dirty-fail";
      libRs = clippyDirtyLib;
      expectFail = true;
    })
    (mkClippyFixture {
      label = "check-clean-pass";
      libRs = clippyCleanLib;
    })
    (mkClippyFixture {
      label = "repair-fixes";
      libRs = clippyDirtyLib;
      action = "repair";
    })
  ];

  # ---- deadnix: scope-flag semantics (conformist#88) -----------------------
  #
  # deadnix carries its behavior in settings.linter.deadnix.options /
  # "repair-options" (--fail and the scope flags), unlike the
  # writeShellApplication linters whose command is self-contained, so these
  # fixtures interpolate command + options explicitly instead of going through
  # mkLinterFixtureCheck. They pin the conformist#88 contract: with the default
  # no-lambda-pattern-names=true, an in-body-unused strict-pattern formal
  # (`{ goMod, src }: src` with a caller passing goMod) is neither flagged by
  # the check nor stripped by the repair, while provably-local dead code (a
  # dead let binding) is still flagged and repaired.
  deadnixMod = lib.evalModule pkgs {
    enableDefaultExcludes = false;
    linters.deadnix.enable = true;
  };
  deadnixSettings = deadnixMod.config.settings.linter.deadnix;
  deadnixCheckCmd = "${deadnixSettings.command} ${nixlib.escapeShellArgs deadnixSettings.options}";
  deadnixRepairCmd = "${deadnixSettings."repair-command"} ${nixlib.escapeShellArgs deadnixSettings."repair-options"}";

  # The conformist#88 shape: goMod is unused in mkThing's own body, the pattern
  # has no `...`, and the call site still passes goMod — removing the formal
  # would make the call fail with "called with unexpected argument".
  deadnixStrictPattern = ''
    let
      mkThing =
        { goMod, src }:
        src;
    in
    mkThing {
      goMod = "unused-by-the-body";
      src = "the-src";
    }
  '';
  deadnixDeadLet = ''
    let
      unusedBinding = 1;
    in
    "kept"
  '';

  deadnixFixtures = [
    # Check: the strict-pattern formal is NOT a finding under the default
    # scope flags (deadnix cannot prove it dead — conformist#88)…
    (pkgs.runCommandLocal "linter-fixture-deadnix-pattern-preserved-pass" { } ''
      mkdir fixture && cd fixture
      cp ${pkgs.writeText "strict-pattern.nix" deadnixStrictPattern} strict-pattern.nix
      if ${deadnixCheckCmd} strict-pattern.nix >out.log 2>&1; then rc=0; else rc=$?; fi
      cat out.log
      if [ "$rc" -ne 0 ]; then
        echo "FIXTURE FAIL: deadnix flagged a strict-pattern formal it cannot prove dead (conformist#88)" >&2
        exit 1
      fi
      touch $out
    '')

    # …while provably-local dead code still fails the check (this also pins
    # --fail being wired in: without it deadnix exits 0 on findings).
    (pkgs.runCommandLocal "linter-fixture-deadnix-dead-let-fail" { } ''
      mkdir fixture && cd fixture
      cp ${pkgs.writeText "dead-let.nix" deadnixDeadLet} dead-let.nix
      if ${deadnixCheckCmd} dead-let.nix >out.log 2>&1; then rc=0; else rc=$?; fi
      cat out.log
      if [ "$rc" -eq 0 ]; then
        echo "FIXTURE FAIL: expected deadnix to reject a dead let binding, but it exited 0" >&2
        exit 1
      fi
      if ! grep -qF "Unused let binding" out.log; then
        echo "FIXTURE FAIL: deadnix output did not contain 'Unused let binding'" >&2
        exit 1
      fi
      touch $out
    '')

    # Repair: --edit removes the dead let binding but leaves the strict-pattern
    # formal in place, and the check passes afterwards (repair-then-check clean).
    (pkgs.runCommandLocal "linter-fixture-deadnix-repair-preserves-pattern" { } ''
      mkdir fixture && cd fixture
      cp ${pkgs.writeText "strict-pattern.nix" deadnixStrictPattern} strict-pattern.nix
      cp ${pkgs.writeText "dead-let.nix" deadnixDeadLet} dead-let.nix
      chmod u+w strict-pattern.nix dead-let.nix

      ${deadnixRepairCmd} strict-pattern.nix dead-let.nix >out.log 2>&1 || true
      cat out.log

      if ! grep -qF "goMod" strict-pattern.nix; then
        echo "FIXTURE FAIL: deadnix repair stripped the strict-pattern formal goMod (conformist#88)" >&2
        cat strict-pattern.nix >&2
        exit 1
      fi
      if grep -qF "unusedBinding" dead-let.nix; then
        echo "FIXTURE FAIL: deadnix repair did not remove the dead let binding" >&2
        cat dead-let.nix >&2
        exit 1
      fi

      if ${deadnixCheckCmd} strict-pattern.nix dead-let.nix >check.log 2>&1; then crc=0; else crc=$?; fi
      cat check.log
      if [ "$crc" -ne 0 ]; then
        echo "FIXTURE FAIL: deadnix check still failed after repair (exit $crc)" >&2
        exit 1
      fi
      touch $out
    '')
  ];

  # ---- Fixtures -----------------------------------------------------------

  fixtures = [
    # eng-versioning: key derivation across go.mod / Cargo.toml / explicit key,
    # plus the rejection paths (conformist#29).
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "go-mod-pass";
      files = {
        "go.mod" = "module example.com/foo\n\ngo 1.26\n";
        "version.env" = "export FOO_VERSION=1.2.3\n";
      };
    })
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "cargo-pass";
      files = {
        "Cargo.toml" = "[package]\nname = \"bar-baz\"\nversion = \"0.1.0\"\n";
        "version.env" = "export BAR_BAZ_VERSION=0.1.0\n";
      };
    })
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "key-override-pass";
      enableModule = {
        key = "WIDGET_VERSION";
      };
      files = {
        # No go.mod and no Cargo.toml: the explicit key must be used verbatim.
        "version.env" = "export WIDGET_VERSION=2.0.0\n";
      };
    })
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "no-manifest-fail";
      files = {
        "version.env" = "export FOO_VERSION=1.2.3\n";
      };
      expectFail = true;
      expectToken = "cannot derive version key";
    })
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "cargo-no-package-name-fail";
      files = {
        # A virtual-workspace Cargo.toml with no [package] table.
        "Cargo.toml" = "[workspace]\nmembers = [\"a\"]\n";
        "version.env" = "export FOO_VERSION=1.2.3\n";
      };
      expectFail = true;
      expectToken = "[package].name not found";
    })
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "wrong-key-fail";
      files = {
        "go.mod" = "module example.com/foo\n";
        # Declares the wrong variable; expected key FOO_VERSION is absent.
        "version.env" = "export BAR_VERSION=1.2.3\n";
      };
      expectFail = true;
      expectToken = "must declare";
    })

    # agents-md: AGENTS.md must stay under the configured character budget
    # (max-chars); no CLAUDE.md present so only the size check is exercised.
    (mkLinterFixtureCheck {
      name = "agents-md";
      label = "under-budget-pass";
      enableModule = {
        max-chars = 20;
      };
      files = {
        "AGENTS.md" = "short and sweet\n";
      };
    })
    (mkLinterFixtureCheck {
      name = "agents-md";
      label = "over-budget-fail";
      enableModule = {
        max-chars = 20;
      };
      files = {
        "AGENTS.md" = "this AGENTS.md is deliberately longer than the twenty character budget\n";
      };
      expectFail = true;
      expectToken = "exceeds the 20-character limit";
    })

    # flake-lock: flake.lock must be committed when flake.nix is present
    # (conformist-nix(7) FLAKE HYGIENE, conformist#11).
    (mkLinterFixtureCheck {
      name = "flake-lock";
      label = "committed-pass";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
        "flake.lock" = "{ \"nodes\": {}, \"version\": 7 }\n";
      };
    })
    (mkLinterFixtureCheck {
      name = "flake-lock";
      label = "missing-fail";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
      };
      expectFail = true;
      expectToken = "flake.lock missing";
    })

    # justfile-debug-recipes: debug/explore recipes must carry a doc comment
    # (eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS, conformist#23).
    (mkLinterFixtureCheck {
      name = "justfile-debug-recipes";
      label = "documented-pass";
      files = {
        "justfile" = ''
          # probe the widget cache for the cache-eviction dev-loop (see #1)
          [group('debug')]
          debug-widget-cache:
              echo hi
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-debug-recipes";
      label = "undocumented-fail";
      files = {
        "justfile" = ''
          [group('debug')]
          debug-widget-cache:
              echo hi
        '';
      };
      expectFail = true;
      expectToken = "no doc comment";
    })

    # justfile-recipe-descriptions: every leaf recipe carries a doc comment
    # (eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS, conformist#17).
    (mkLinterFixtureCheck {
      name = "justfile-recipe-descriptions";
      label = "documented-pass";
      files = {
        "justfile" = ''
          # builds the thing
          build-thing:
              echo hi
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-recipe-descriptions";
      label = "undocumented-leaf-fail";
      files = {
        "justfile" = ''
          build-thing:
              echo hi
        '';
      };
      expectFail = true;
      expectToken = "no doc comment";
    })
    (mkLinterFixtureCheck {
      name = "justfile-recipe-descriptions";
      label = "exempts-aggregate-and-debug-pass";
      files = {
        # Aggregate (no body) is self-documenting; debug recipe is #23's job:
        # both are exempt even when undocumented, so this passes.
        "justfile" = ''
          # documented leaf
          build-thing:
              echo hi

          agg: build-thing

          [group('debug')]
          debug-thing:
              echo hi
        '';
      };
    })

    # justfile-task-hierarchy: no leaf belongs to more than one aggregate
    # (eng-design_patterns-justfile(7) TASK HIERARCHY, conformist#17).
    (mkLinterFixtureCheck {
      name = "justfile-task-hierarchy";
      label = "single-aggregate-pass";
      files = {
        "justfile" = ''
          build: build-go build-nix

          build-go:
              echo go

          build-nix:
              echo nix
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-task-hierarchy";
      label = "orphan-leaf-pass";
      files = {
        # A leaf in no aggregate is a legitimate standalone recipe (release, tag,
        # run-nix): the upper-bound rule must NOT flag it.
        "justfile" = ''
          release-thing:
              echo release
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-task-hierarchy";
      label = "multi-aggregate-fail";
      files = {
        "justfile" = ''
          build: shared
          verify: shared

          shared:
              echo hi
        '';
      };
      expectFail = true;
      expectToken = "at most one aggregate";
    })
    (mkLinterFixtureCheck {
      name = "justfile-task-hierarchy";
      label = "pipeline-orphan-fail";
      files = {
        # A pipeline-verb leaf (build) in no aggregate is unreachable from default
        # — the tightened lower bound must flag it.
        "justfile" = ''
          build-go:
              echo go
        '';
      };
      expectFail = true;
      expectToken = "exactly one aggregate";
    })

    # justfile-leaf-noun: a leaf must be verb-noun, not a bare verb
    # (conformist-justfile(7) AGGREGATES AND LEAVES, conformist#17).
    (mkLinterFixtureCheck {
      name = "justfile-leaf-noun";
      label = "verb-noun-pass";
      files = {
        "justfile" = ''
          build-go:
              echo hi
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-leaf-noun";
      label = "bare-verb-fail";
      files = {
        "justfile" = ''
          build:
              echo hi
        '';
      };
      expectFail = true;
      expectToken = "bare verb";
    })
    (mkLinterFixtureCheck {
      name = "justfile-leaf-noun";
      label = "tag-exempt-pass";
      files = {
        # `tag` is a verb-noun-exempt release recipe even as a single-segment leaf.
        "justfile" = ''
          tag:
              echo tag
        '';
      };
    })

    # justfile-aggregate-comments: an aggregate must not carry a doc comment
    # (conformist-justfile(7) AGGREGATES AND LEAVES, conformist#17).
    (mkLinterFixtureCheck {
      name = "justfile-aggregate-comments";
      label = "uncommented-pass";
      files = {
        "justfile" = ''
          build: build-go

          # compiles go
          build-go:
              echo hi
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-aggregate-comments";
      label = "commented-fail";
      files = {
        "justfile" = ''
          # builds everything
          build: build-go

          # compiles go
          build-go:
              echo hi
        '';
      };
      expectFail = true;
      expectToken = "doc comment";
    })

    # justfile-default: `default` is first and lists only aggregates
    # (conformist-justfile(7) DEFAULT RECIPE / AGGREGATES AND LEAVES). The
    # aggregate-pass case is the conformist#51 regression: a BACKSLASH-CONTINUED
    # aggregate must not be misread as a leaf-with-a-body (the old awk indent
    # heuristic flagged it; the `just --dump` rewrite parses it correctly).
    (mkLinterFixtureCheck {
      name = "justfile-default";
      label = "backslash-aggregate-pass";
      files = {
        "justfile" = ''
          default: test

          test: \
              test-go \
              test-bats

          test-go:
              echo go

          test-bats:
              echo bats
        '';
      };
    })
    (mkLinterFixtureCheck {
      name = "justfile-default";
      label = "lists-leaf-fail";
      files = {
        # `default` depends on a leaf that has a body — not an aggregate.
        "justfile" = ''
          default: run-thing

          run-thing:
              echo x
        '';
      };
      expectFail = true;
      expectToken = "lists leaf recipe";
    })
    (mkLinterFixtureCheck {
      name = "justfile-default";
      label = "first-not-default-fail";
      files = {
        "justfile" = ''
          build-go:
              echo go

          default: build-go
        '';
      };
      expectFail = true;
      expectToken = "first recipe must be 'default'";
    })
  ];

  allFixtures = fixtures ++ gitRemotesFixtures ++ deadnixFixtures;
in
builtins.listToAttrs (
  map (d: {
    inherit (d) name;
    value = d;
  }) allFixtures
)
// {
  # Aggregate: a link farm that forces every fixture to build. The cheap recipe
  # `just verify-linter-fixtures` builds this one path instead of the full
  # `nix flake check` (which would also build the ~130 registry smoke checks).
  # Deliberately EXCLUDES the clippy fixtures (below) so the merge-hook lane
  # never pulls a Rust toolchain.
  linter-fixtures = pkgs.linkFarmFromDrvs "linter-fixtures" allFixtures;

  # clippy fixtures live in their own aggregate, built on demand by
  # `just explore-clippy-fixture` (NOT in the default verify lane), so the Rust
  # toolchain stays out of conformist's CI. See the clippy block above.
  clippy-fixtures = pkgs.linkFarmFromDrvs "clippy-fixtures" clippyFixtures;
}
