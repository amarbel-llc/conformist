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

  # NOTE: the justfile-* linter fixtures used to live here. They moved to just-us
  # with the linters themselves — those rules read the fork-only
  # `just --dump --dump-format model`, so their fixtures need a just-us build to
  # be meaningful and belong in the repo that ships both.

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
  # evalPkgs    : pkgs the linter MODULE is evaluated against (default: pkgs).
  #               Lets a fixture exercise a module's degraded wiring when an
  #               overlay-provided tool is absent, e.g.
  #               `builtins.removeAttrs pkgs [ "gomod2nix" ]` (conformist#93).
  #               The fixture derivation itself always builds with the real pkgs.
  mkLinterFixtureCheck =
    {
      name,
      label,
      enableModule ? { },
      files,
      expectFail ? false,
      expectToken ? null,
      evalPkgs ? pkgs,
    }:
    let
      mod = lib.evalModule evalPkgs {
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

  # ---- agents-md: nested-CLAUDE.md walk scoping (conformist#95) -----------
  #
  # The nested-CLAUDE.md walk is scoped to git-TRACKED files when a live
  # worktree is present, so a gitignored child checkout never surfaces a
  # finding, and `exclude-paths` opts a legitimately-named payload out
  # explicitly. This needs real git state (tracked vs. merely-on-disk), which
  # mkLinterFixtureCheck's bare file-copy sandbox can't express — so, like
  # git-remotes below, this `git init`s a throwaway repo instead.
  #
  # label       : fixture label (derivation suffix)
  # enableModule: extra options merged into `linters.agents-md` (e.g.
  #               { exclude-paths = [ ... ]; })
  # setup       : shell snippet run after `git init -q` to create/stage files
  # expectFail  : true => the check MUST exit non-zero
  # expectToken : optional substring the check output MUST contain
  mkAgentsMdWalkFixture =
    {
      label,
      enableModule ? { },
      setup,
      expectFail ? false,
      expectToken ? null,
    }:
    let
      mod = lib.evalModule pkgs {
        enableDefaultExcludes = false;
        linters.agents-md = {
          enable = true;
        }
        // enableModule;
      };
      cmd = mod.config.settings.linter.agents-md.command;

      assertExit =
        if expectFail then
          ''
            if [ "$rc" -eq 0 ]; then
              echo "FIXTURE FAIL: expected agents-md to reject ${label}, but it exited 0" >&2
              exit 1
            fi
          ''
        else
          ''
            if [ "$rc" -ne 0 ]; then
              echo "FIXTURE FAIL: expected agents-md to pass ${label}, but it exited $rc" >&2
              exit 1
            fi
          '';

      assertToken = nixlib.optionalString (expectToken != null) ''
        if ! grep -qF ${nixlib.escapeShellArg expectToken} out.log; then
          echo "FIXTURE FAIL: agents-md ${label} output did not contain ${nixlib.escapeShellArg expectToken}" >&2
          exit 1
        fi
      '';
    in
    pkgs.runCommandLocal "linter-fixture-agents-md-walk-${label}"
      {
        nativeBuildInputs = [ pkgs.git ];
      }
      ''
        export HOME="$PWD/home"
        mkdir -p "$HOME"
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
        mkdir fixture && cd fixture
        git init -q

        ${setup}

        if ${cmd} >out.log 2>&1; then rc=0; else rc=$?; fi
        cat out.log

        ${assertExit}
        ${assertToken}

        touch $out
      '';

  agentsMdWalkFixtures = [
    # A nested CLAUDE.md that sits in a gitignored subtree (never `git add`ed —
    # eng's vendored `repos/**` child checkouts are the motivating case) must
    # not surface a finding: the walk only sees git-tracked paths.
    (mkAgentsMdWalkFixture {
      label = "gitignored-nested-pass";
      setup = ''
        mkdir -p vendor/child
        printf 'vendor/\n' > .gitignore
        printf 'nested\n' > vendor/child/CLAUDE.md
        git add .gitignore
      '';
    })

    # A nested CLAUDE.md that IS git-tracked must still be flagged.
    (mkAgentsMdWalkFixture {
      label = "tracked-nested-fail";
      setup = ''
        mkdir -p sub
        printf 'nested\n' > sub/CLAUDE.md
        git add sub/CLAUDE.md
      '';
      expectFail = true;
      expectToken = "nested sub/CLAUDE.md should be migrated";
    })

    # conformist#111: a nested directory that is ALREADY migrated — AGENTS.md
    # plus the tracked CLAUDE.md -> AGENTS.md back-compat symlink this linter's
    # own repair-command produces — must pass. `git ls-files` lists that tracked
    # symlink where the pre-#95 `find -type f` walk implicitly skipped it, so
    # without the root branch's symlink handling mirrored into the nested loop
    # every migrated subdirectory reported a permanent, unclearable finding.
    (mkAgentsMdWalkFixture {
      label = "nested-migrated-symlink-pass";
      setup = ''
        mkdir -p sub
        printf 'nested orientation\n' > sub/AGENTS.md
        ln -s AGENTS.md sub/CLAUDE.md
        git add sub/AGENTS.md sub/CLAUDE.md
      '';
    })

    # …but a nested symlink pointing somewhere OTHER than its sibling AGENTS.md
    # is still a finding (parity with the root branch's symlink handling).
    (mkAgentsMdWalkFixture {
      label = "nested-symlink-wrong-target-fail";
      setup = ''
        mkdir -p sub
        printf 'nested orientation\n' > sub/ORIENTATION.md
        ln -s ORIENTATION.md sub/CLAUDE.md
        git add sub/ORIENTATION.md sub/CLAUDE.md
      '';
      expectFail = true;
      expectToken = "nested sub/CLAUDE.md is a symlink to 'ORIENTATION.md'";
    })

    # …and one that dangles (points at an AGENTS.md that isn't there) too.
    (mkAgentsMdWalkFixture {
      label = "nested-symlink-broken-fail";
      setup = ''
        mkdir -p sub
        ln -s AGENTS.md sub/CLAUDE.md
        git add sub/CLAUDE.md
      '';
      expectFail = true;
      expectToken = "nested sub/CLAUDE.md -> AGENTS.md but AGENTS.md is missing";
    })

    # The fleet's OTHER migration shape (conformist#111 follow-up): rename the
    # nested files OUTRIGHT, keeping a back-compat symlink only at the repo
    # root — cutting-garden's d3b94ca, as against maneater's
    # rename-plus-nested-compat-symlink covered above. Here the nested walk
    # must yield nothing at all: the tracked ROOT CLAUDE.md symlink IS handed
    # to the loop by `git ls-files`, and is skipped by the `$f = "CLAUDE.md"`
    # guard before any symlink logic runs, while the nested AGENTS.md siblings
    # are never enumerated. Pinned because half the fleet depends on this shape
    # staying green and nothing else in this matrix exercises it — every other
    # agents-md fixture either has no CLAUDE.md at all or has a nested one.
    #
    # Scope, so a later reader doesn't over-trust it: this is a SHAPE-level
    # guard, not a guard on the `$f = "CLAUDE.md"` line. Deleting that line
    # would leave this fixture green, because the nested symlink branch then
    # accepts the root symlink on identical terms. What it does catch is the
    # walk regressing to report anything at all for a tree in this shape.
    (mkAgentsMdWalkFixture {
      label = "root-symlink-nested-agents-only-pass";
      setup = ''
        printf 'root orientation\n' > AGENTS.md
        ln -s AGENTS.md CLAUDE.md
        mkdir -p plugins/caldav internal/capture_plugin
        printf 'plugin orientation\n' > plugins/caldav/AGENTS.md
        printf 'internal orientation\n' > internal/capture_plugin/AGENTS.md
        git add AGENTS.md CLAUDE.md plugins/caldav/AGENTS.md internal/capture_plugin/AGENTS.md
      '';
    })

    # A tracked nested CLAUDE.md whose path is explicitly excluded (a deployed
    # dotfile payload where the filename IS the product) must pass.
    (mkAgentsMdWalkFixture {
      label = "tracked-excluded-pass";
      enableModule = {
        exclude-paths = [ "rcm/claude/CLAUDE.md" ];
      };
      setup = ''
        mkdir -p rcm/claude
        printf 'payload\n' > rcm/claude/CLAUDE.md
        git add rcm/claude/CLAUDE.md
      '';
    })
  ];

  # ---- git-remotes: a live-git linter (check reads remotes, repair mutates) ----
  #
  # Unlike the pure whole-tree checks above, git-remotes reads live git state
  # (`git remote -v`) and its repair rewrites it (`git remote set-url`). The
  # fixture therefore `git init`s a throwaway repo, `git remote add`s the given
  # URLs, then runs the check or the repair and asserts. git init / remote ops
  # are offline, so this still runs in the pure runCommandLocal sandbox
  # (conformist#68).
  #
  # label        : fixture label (derivation suffix)
  # remotes      : attrset remote-name -> URL to `git remote add`
  # action       : "check" (run the read-only command) or "repair" (run repair)
  # enableModule : extra options merged into `linters.git-remotes` (e.g.
  #                { allowed-hosts = [ "github.com" ]; }) — each fixture evals
  #                its own module instance so different fixtures can exercise
  #                different canonical-host/allowed-hosts configs
  # expectFail   : check action only — true => check MUST exit non-zero
  # expectToken  : optional substring the action output MUST contain
  # expectRemotes: repair action only — attrset remote-name -> expected fetch URL
  #                after repair (asserted via `git remote get-url`)
  # recheckPasses: repair action only — whether the read-only check MUST pass
  #                after the repair (false when a remote's transport AND/OR
  #                origin's host stays outside what repair can fix / what this
  #                fixture's module config accepts)
  mkGitRemotesFixture =
    {
      label,
      remotes,
      action ? "check",
      enableModule ? { },
      expectFail ? false,
      expectToken ? null,
      expectRemotes ? { },
      recheckPasses ? true,
      # Extra `git config <key> <value>` pairs applied after the remotes are
      # added. Exists so a fixture can install a `url.<base>.insteadOf` rewrite,
      # which is local machine policy rather than repo state and so cannot be
      # expressed through `remotes` above.
      gitConfig ? { },
    }:
    let
      mod = lib.evalModule pkgs {
        enableDefaultExcludes = false;
        linters.git-remotes = {
          enable = true;
        }
        // enableModule;
      };
      gitRemotesCheck = mod.config.settings.linter.git-remotes.command;
      gitRemotesRepair = mod.config.settings.linter.git-remotes."repair-command";

      addRemotes = nixlib.concatStringsSep "\n" (
        nixlib.mapAttrsToList (
          name: url: "git remote add ${nixlib.escapeShellArg name} ${nixlib.escapeShellArg url}"
        ) remotes
      );

      setGitConfig = nixlib.concatStringsSep "\n" (
        nixlib.mapAttrsToList (
          key: value: "git config ${nixlib.escapeShellArg key} ${nixlib.escapeShellArg value}"
        ) gitConfig
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
                echo "FIXTURE FAIL: git-remotes ${label}: check unexpectedly passed after repair (a non-canonical-host or unfixable-transport remote should stay flagged)" >&2
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
        ${setGitConfig}

        ${if action == "repair" then repairBody else checkBody}

        touch $out
      '';

  gitRemotesFixtures = [
    # Check, transport rule: a non-SSH remote is flagged regardless of host —
    # use the canonical forge host here so this is unambiguously a transport
    # failure, not a host failure (the host fixtures below cover that rule).
    (mkGitRemotesFixture {
      label = "check-https-fail";
      remotes.origin = "https://code.linenisgreat.com/hyphence.git";
      expectFail = true;
      expectToken = "non-SSH remote";
    })

    # Check, default canonical host: origin on the forge via SSH passes with
    # no extra config (code.linenisgreat.com is canonical-host's default).
    (mkGitRemotesFixture {
      label = "check-forge-ssh-pass";
      remotes.origin = "git@code.linenisgreat.com:hyphence.git";
    })

    # Check, insteadOf: a CORRECT ssh remote that a local `url.<base>.insteadOf`
    # rewrites to https at use time MUST still pass. Both `git remote -v` and
    # `git remote get-url` apply that rewriting, so reading either made this rule
    # fail a repo whose committed remotes are right, citing a URL absent from
    # .git/config — which broke every merge for a session using ephemeral https
    # push credentials. insteadOf is per-machine policy; the rule is about what
    # the repo DECLARES, so the check reads `git config --get`.
    #
    # This fixture is the discriminator: it FAILS against the pre-fix linter and
    # passes after. `just debug-git-insteadof` shows the underlying git behavior.
    (mkGitRemotesFixture {
      label = "check-ssh-with-insteadof-rewrite-pass";
      remotes.origin = "git@code.linenisgreat.com:hyphence.git";
      gitConfig = {
        "url.https://code.linenisgreat.com/.insteadOf" = "git@code.linenisgreat.com:";
      };
    })

    # ...and the rewrite must not become a way to HIDE a genuinely non-SSH
    # remote: an https remote stays flagged even when an insteadOf maps it to
    # ssh at use time. Guards against the fix over-correcting into "trust
    # whatever local config says".
    (mkGitRemotesFixture {
      label = "check-https-with-insteadof-to-ssh-still-fails";
      remotes.origin = "https://code.linenisgreat.com/hyphence.git";
      gitConfig = {
        "url.git@code.linenisgreat.com:.insteadOf" = "https://code.linenisgreat.com/";
      };
      expectFail = true;
      expectToken = "non-SSH remote";
    })

    # Transport rule still covers a separately-declared push URL, which the old
    # `git remote -v` scan caught via its (push) line.
    (mkGitRemotesFixture {
      label = "check-non-ssh-pushurl-fail";
      remotes.origin = "git@code.linenisgreat.com:hyphence.git";
      gitConfig = {
        "remote.origin.pushurl" = "https://code.linenisgreat.com/hyphence.git";
      };
      expectFail = true;
      expectToken = "non-SSH remote";
    })

    # Check, host rule: origin on github.com via SSH — transport is fine, but
    # without an allowlist entry the host itself is rejected (this is the new
    # enforcement — previously any SSH host passed unconditionally).
    (mkGitRemotesFixture {
      label = "check-github-ssh-fails-without-allowlist";
      remotes.origin = "git@github.com:amarbel-llc/hyphence.git";
      expectFail = true;
      expectToken = "canonical host is 'code.linenisgreat.com'";
    })

    # Check, allowlist: the same github.com SSH origin passes once this repo
    # declares allowed-hosts (the circus-shaped case — deliberately still on
    # GitHub, but still checked/checkable against that fact).
    (mkGitRemotesFixture {
      label = "check-github-ssh-pass-with-allowlist";
      remotes.origin = "git@github.com:amarbel-llc/hyphence.git";
      enableModule = {
        allowed-hosts = [ "github.com" ];
      };
    })

    # Repair, forge host: each code.linenisgreat.com network transport
    # rewrites to the flat/owner-less SSH form, the recheck then passes under
    # the default (forge-canonical) config, and a missing `.git` suffix is
    # normalized in.
    (mkGitRemotesFixture {
      label = "repair-forge-https";
      action = "repair";
      remotes.origin = "https://code.linenisgreat.com/hyphence.git";
      expectToken = "rewrote remote 'origin'";
      expectRemotes.origin = "git@code.linenisgreat.com:hyphence.git";
    })
    (mkGitRemotesFixture {
      label = "repair-forge-https-no-dotgit";
      action = "repair";
      remotes.origin = "https://code.linenisgreat.com/hyphence";
      expectRemotes.origin = "git@code.linenisgreat.com:hyphence.git";
    })

    # Repair, github host WITH allowlist: transport normalizes to SSH exactly
    # as it always has, and because this fixture's module declares
    # allowed-hosts = [ "github.com" ], the recheck passes too — repair's
    # transport fix and the host check compose cleanly for an allowlisted repo.
    (mkGitRemotesFixture {
      label = "repair-github-http-with-allowlist";
      action = "repair";
      remotes.origin = "http://github.com/amarbel-llc/hyphence.git";
      enableModule = {
        allowed-hosts = [ "github.com" ];
      };
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
    })

    # Repair, github host WITHOUT allowlist: repair still normalizes the
    # transport (repair runs the same github.com rewrite regardless of
    # canonical-host/allowed-hosts — an allowlisted repo isn't a prerequisite
    # for transport hygiene), but the recheck now fails on the host rule since
    # this fixture's module uses the default (empty) allowed-hosts. Repair
    # alone cannot launder a wrong host, same as it already couldn't launder a
    # non-github/non-forge host below.
    (mkGitRemotesFixture {
      label = "repair-github-git-protocol-without-allowlist";
      action = "repair";
      remotes.origin = "git://github.com/amarbel-llc/hyphence.git";
      expectRemotes.origin = "git@github.com:amarbel-llc/hyphence.git";
      recheckPasses = false;
    })

    # Repair idempotency: an already-SSH forge remote is a no-op.
    (mkGitRemotesFixture {
      label = "repair-forge-ssh-noop";
      action = "repair";
      remotes.origin = "git@code.linenisgreat.com:hyphence.git";
      expectToken = "no github.com/code.linenisgreat.com non-SSH remotes to rewrite";
      expectRemotes.origin = "git@code.linenisgreat.com:hyphence.git";
    })

    # Repair selectivity: a host that's neither github.com nor
    # code.linenisgreat.com has no canonical SSH form, so repair leaves it
    # alone and the read-only check keeps reporting it (transport AND host).
    (mkGitRemotesFixture {
      label = "repair-leaves-non-github-non-forge";
      action = "repair";
      remotes.origin = "https://gitlab.com/amarbel-llc/hyphence.git";
      expectToken = "no github.com/code.linenisgreat.com non-SSH remotes to rewrite";
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
      enableModule = {
        allowed-hosts = [ "github.com" ];
      };
      expectRemotes = {
        origin = "git@github.com:amarbel-llc/hyphence.git";
        upstream = "https://gitlab.com/up/stream.git";
      };
      # origin is on the allowlisted github.com host, but upstream's gitlab.com
      # non-SSH transport still fails the transport rule (which applies to
      # every remote, not just origin) — so the recheck must still fail.
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
  deadnixRepairCmd = "${deadnixSettings."repair-command"} ${
    nixlib.escapeShellArgs deadnixSettings."repair-options"
  }";

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

  # ---- merge drivers (conformist-git(7) MERGE DRIVERS) --------------------
  #
  # These do NOT go through mkLinterFixtureCheck: the drivers are not linter
  # commands. git invokes them, so the only fixture that proves anything is a
  # real `git merge` with the driver registered the way eng's git config will
  # register it.
  mergeDrivers = import ./merge-drivers.nix { inherit pkgs; };
  codegenDriver = nixlib.getExe mergeDrivers.codegen-header;
  flakeLockDriver = nixlib.getExe mergeDrivers.flake-lock;

  # A generated Go file: line 1 is the generator stamp, the rest is the body.
  genStampedFile =
    stamp: body: "// Code generated by tommy ${stamp}; DO NOT EDIT.\n\npackage gen\n\n${body}";
  genBody = "func Answer() int { return 42 }\n";

  # label         : fixture label (derivation suffix)
  # oursBody      : body on the merge target (main)
  # theirsBody    : body on the merged-in branch
  # registerDriver: whether to install the merge.<name>.driver git config.
  #                 The false variant is the CONTROL — see expectConflict.
  # expectConflict: true => `git merge` MUST fail with markers left behind
  mkCodegenMergeFixture =
    {
      label,
      oursBody ? genBody,
      theirsBody ? genBody,
      registerDriver ? true,
      expectConflict ? false,
    }:
    let
      baseFile = pkgs.writeText "gen-base.go" (genStampedFile "v1" genBody);
      oursFile = pkgs.writeText "gen-ours.go" (genStampedFile "v3" oursBody);
      theirsFile = pkgs.writeText "gen-theirs.go" (genStampedFile "v2" theirsBody);

      registration = nixlib.optionalString registerDriver ''
        git config merge.conformist-codegen-header.name 'conformist codegen header'
        git config merge.conformist-codegen-header.driver '${codegenDriver} %O %A %B %L %P'
      '';

      assertOutcome =
        if expectConflict then
          ''
            if [ "$rc" -eq 0 ]; then
              echo "FIXTURE FAIL: expected the ${label} merge to conflict, but it succeeded" >&2
              cat gen.go >&2
              exit 1
            fi
            if ! grep -q '<<<<<<<' gen.go; then
              echo "FIXTURE FAIL: ${label} merge failed but left no conflict markers in gen.go" >&2
              cat gen.go >&2
              exit 1
            fi
          ''
        else
          ''
            if [ "$rc" -ne 0 ]; then
              echo "FIXTURE FAIL: expected the ${label} merge to resolve, but it exited $rc" >&2
              cat gen.go >&2
              exit 1
            fi
            # The resolution must be exactly ours' stamp over the shared body —
            # not merely "no markers", which a driver that emitted garbage would
            # also satisfy.
            if ! diff -u ${oursFile} gen.go; then
              echo "FIXTURE FAIL: ${label} resolved to something other than ours' stamp + body" >&2
              exit 1
            fi
          '';
    in
    pkgs.runCommandLocal "merge-driver-fixture-codegen-${label}"
      {
        nativeBuildInputs = [
          pkgs.git
          pkgs.diffutils
        ];
      }
      ''
        export HOME="$PWD/home"
        mkdir -p "$HOME"
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
        mkdir repo && cd repo
        git init -q -b main
        git config user.email fixture@example.invalid
        git config user.name fixture
        ${registration}

        # .gitattributes is the per-repo half the git-merge-drivers linter
        # enforces; the git config above is the per-machine half eng owns.
        printf 'gen.go merge=conformist-codegen-header\n' >.gitattributes
        install -m 644 ${baseFile} gen.go
        git add .gitattributes gen.go
        git commit -qm base

        git checkout -q -b feature
        install -m 644 ${theirsFile} gen.go
        git commit -qam theirs

        git checkout -q main
        install -m 644 ${oursFile} gen.go
        git commit -qam ours

        # Both sides rewrote line 1 (the stamp). Git's built-in 3-way merge
        # ALWAYS conflicts on that, which is what makes the registered-driver
        # case a real discriminator rather than a merge that would have
        # succeeded anyway — the registerDriver=false control below pins it.
        if git merge --no-edit feature >merge.log 2>&1; then rc=0; else rc=$?; fi
        cat merge.log

        ${assertOutcome}
        touch $out
      '';

  # label      : fixture label (derivation suffix)
  # setup      : shell run inside the initialized repo before invoking the driver
  # expectToken: substring the refusal message MUST contain
  mkFlakeLockDriverFixture =
    {
      label,
      setup,
      expectToken,
    }:
    pkgs.runCommandLocal "merge-driver-fixture-flake-lock-${label}"
      {
        nativeBuildInputs = [ pkgs.git ];
      }
      ''
        export HOME="$PWD/home"
        mkdir -p "$HOME"
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
        mkdir repo && cd repo
        git init -q -b main

        ${setup}

        printf '{"nodes":{},"root":"root","version":7}\n' >base.lock
        printf '{"nodes":{},"root":"root","version":7}\n' >ours.lock
        printf '{"nodes":{},"root":"root","version":7}\n' >theirs.lock

        if ${flakeLockDriver} base.lock ours.lock theirs.lock flake.lock >out.log 2>&1; then
          rc=0
        else
          rc=$?
        fi
        cat out.log

        # Every one of these is a FAIL-CLOSED path: the driver must decline and
        # let git record an ordinary conflict, never resolve on a guess.
        if [ "$rc" -eq 0 ]; then
          echo "FIXTURE FAIL: flake-lock driver ${label} resolved when it should have refused" >&2
          exit 1
        fi
        if ! grep -qF ${nixlib.escapeShellArg expectToken} out.log; then
          echo "FIXTURE FAIL: flake-lock driver ${label} did not report ${nixlib.escapeShellArg expectToken}" >&2
          exit 1
        fi
        touch $out
      '';

  mergeDriverFixtures = [
    # The incident shape: both sides restamped the generator header, bodies
    # identical. Must merge clean, resolving the stamp to ours.
    (mkCodegenMergeFixture { label = "stamp-only-resolves"; })

    # THE CONTROL. Identical inputs, driver NOT registered — git's built-in
    # merge must conflict. Without this, the fixture above could be passing for
    # an incidental reason (e.g. a .gitattributes typo silently disabling the
    # binding) and would still look green.
    (mkCodegenMergeFixture {
      label = "stamp-only-conflicts-without-driver";
      registerDriver = false;
      expectConflict = true;
    })

    # A real body divergence on top of the restamp is NOT the driver's to
    # resolve: it must stay a conflict for a human.
    (mkCodegenMergeFixture {
      label = "body-divergence-still-conflicts";
      oursBody = "func Answer() int { return 43 }\n";
      theirsBody = "func Answer() int { return 44 }\n";
      expectConflict = true;
    })

    # flake-lock driver, fail-closed paths. The happy path needs a real `nix`
    # and so lives outside the sandbox, in `just explore-merge-driver-flake-lock`.
    (mkFlakeLockDriverFixture {
      label = "no-flake-nix";
      setup = "";
      expectToken = "no flake.nix beside";
    })
    (mkFlakeLockDriverFixture {
      label = "unresolved-flake-nix";
      # Git gives no ordering guarantee between flake.nix's and flake.lock's
      # drivers, so a lock regenerated against a marker-laden flake.nix is a
      # real hazard, not a hypothetical one.
      setup = ''
        {
          printf '{\n'
          printf '<<<<<<< HEAD\n'
          printf '  description = "ours";\n'
          printf '=======\n'
          printf '  description = "theirs";\n'
          printf '>>>>>>> feature\n'
          printf '}\n'
        } >flake.nix
      '';
      expectToken = "still has conflict markers";
    })
    (mkFlakeLockDriverFixture {
      label = "no-nix-on-path";
      # The build sandbox has no `nix` on PATH, which is exactly the condition
      # this asserts: without a way to regenerate, refuse rather than pick a side.
      setup = ''printf '{ outputs = { ... }: { }; }\n' >flake.nix'';
      expectToken = "nix is not on PATH";
    })
  ];

  # ---- git-merge-drivers linter (the .gitattributes half) -----------------
  #
  # The repair mutates .gitattributes, so unlike mkLinterFixtureCheck's
  # read-only store copies this needs a writable tree.
  gitMergeDriversMod = lib.evalModule pkgs {
    enableDefaultExcludes = false;
    linters.git-merge-drivers.enable = true;
  };
  gitMergeDriversCheck = gitMergeDriversMod.config.settings.linter.git-merge-drivers.command;
  gitMergeDriversRepair =
    gitMergeDriversMod.config.settings.linter.git-merge-drivers."repair-command";

  # label        : fixture label (derivation suffix)
  # files        : attrset of relpath -> content written into the fixture tree
  # action       : "check" or "repair"
  # expectFail   : check action only — true => check MUST exit non-zero
  # expectToken  : optional substring the action output MUST contain
  # expectContent: repair action only — substrings .gitattributes MUST contain
  mkGitMergeDriversFixture =
    {
      label,
      files,
      action ? "check",
      expectFail ? false,
      expectToken ? null,
      expectContent ? [ ],
    }:
    let
      assertToken = nixlib.optionalString (expectToken != null) ''
        if ! grep -qF ${nixlib.escapeShellArg expectToken} out.log; then
          echo "FIXTURE FAIL: git-merge-drivers ${label} output did not contain ${nixlib.escapeShellArg expectToken}" >&2
          exit 1
        fi
      '';

      assertContent = nixlib.concatMapStrings (s: ''
        if ! grep -qF ${nixlib.escapeShellArg s} .gitattributes; then
          echo "FIXTURE FAIL: git-merge-drivers ${label}: .gitattributes lacks ${nixlib.escapeShellArg s}" >&2
          cat .gitattributes >&2
          exit 1
        fi
      '') expectContent;

      checkBody = ''
        if ${gitMergeDriversCheck} >out.log 2>&1; then rc=0; else rc=$?; fi
        cat out.log
        ${
          if expectFail then
            ''
              if [ "$rc" -eq 0 ]; then
                echo "FIXTURE FAIL: expected git-merge-drivers to reject ${label}, but it exited 0" >&2
                exit 1
              fi
            ''
          else
            ''
              if [ "$rc" -ne 0 ]; then
                echo "FIXTURE FAIL: expected git-merge-drivers to pass ${label}, but it exited $rc" >&2
                exit 1
              fi
            ''
        }
        ${assertToken}
      '';

      repairBody = ''
        ${gitMergeDriversRepair} >out.log 2>&1
        cat out.log
        # Second pass proves idempotency: it must add nothing and stay exit 0.
        cp .gitattributes .gitattributes.first
        ${gitMergeDriversRepair} >/dev/null 2>&1
        if ! diff -u .gitattributes.first .gitattributes; then
          echo "FIXTURE FAIL: git-merge-drivers repair ${label} was not idempotent" >&2
          exit 1
        fi
        ${assertToken}
        ${assertContent}
        # Repair must make the read-only check pass.
        if ${gitMergeDriversCheck} >check.log 2>&1; then crc=0; else crc=$?; fi
        cat check.log
        if [ "$crc" -ne 0 ]; then
          echo "FIXTURE FAIL: git-merge-drivers ${label}: check still failed after repair (exit $crc)" >&2
          exit 1
        fi
      '';
    in
    pkgs.runCommandLocal "linter-fixture-git-merge-drivers-${label}"
      {
        nativeBuildInputs = [ pkgs.diffutils ];
      }
      ''
        mkdir fixture && cd fixture
        ${writeFixtureFiles files}
        # Store copies land 0444; the repair rewrites .gitattributes.
        chmod -R u+w .

        ${if action == "repair" then repairBody else checkBody}
        touch $out
      '';

  gitMergeDriversFixtures = [
    # A flake repo whose .gitattributes already binds flake.lock passes.
    (mkGitMergeDriversFixture {
      label = "bound-pass";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
        ".gitattributes" = "flake.lock merge=conformist-flake-lock\n";
      };
    })

    # …and one that does not is a finding.
    (mkGitMergeDriversFixture {
      label = "unbound-fail";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
      };
      expectFail = true;
      expectToken = "flake.lock merge=conformist-flake-lock";
    })

    # The `when-file` gate: no flake.nix means no flake.lock binding is
    # required, so a repo with neither passes rather than nagging.
    (mkGitMergeDriversFixture {
      label = "no-flake-pass";
      files = {
        ".gitattributes" = "* text=auto\n";
      };
    })

    # Binding the pattern to some OTHER driver is still a finding — the check
    # matches the pattern/driver pair, not merely the presence of the path.
    (mkGitMergeDriversFixture {
      label = "wrong-driver-fail";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
        ".gitattributes" = "flake.lock merge=ours\n";
      };
      expectFail = true;
      expectToken = "flake.lock merge=conformist-flake-lock";
    })

    # Repair creates .gitattributes when absent.
    (mkGitMergeDriversFixture {
      label = "repair-creates";
      action = "repair";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
      };
      expectContent = [ "flake.lock merge=conformist-flake-lock" ];
    })

    # Repair appends without clobbering unrelated rules — an existing
    # .gitattributes routinely carries text/linguist/LFS lines that must
    # survive, and a file with no trailing newline must not get its last line
    # glued to ours.
    (mkGitMergeDriversFixture {
      label = "repair-preserves-existing";
      action = "repair";
      files = {
        "flake.nix" = "{ outputs = { ... }: { }; }\n";
        ".gitattributes" = "* text=auto\nzz-tests/** merge=ours linguist-generated";
      };
      expectContent = [
        "* text=auto"
        "zz-tests/** merge=ours linguist-generated"
        "flake.lock merge=conformist-flake-lock"
      ];
    })
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
    (mkLinterFixtureCheck {
      name = "eng-versioning";
      label = "missing-version-env-fail";
      files = {
        # A flake-bearing Go repo with no version.env at all. This error path
        # was unreachable while the linter gated on version.env itself
        # (includes suppressed the run exactly when the file was missing);
        # the gate now includes flake.nix, making absence detectable.
        "flake.nix" = "{ }\n";
        "go.mod" = "module example.com/foo\n";
      };
      expectFail = true;
      expectToken = "version.env missing";
    })

    # gomod2nix missing-tool fallback (conformist#93): the module is evaluated
    # against a pkgs WITHOUT the (igloo-overlay-provided) gomod2nix attribute,
    # proving both that enabling the linter no longer crashes eval and that the
    # degraded wiring behaves — silent no-op without a go.mod, loud actionable
    # failure with one. (The REAL check still has no fixture: it shells out to
    # gomod2nix, which the pure sandbox cannot run — see nix/linters/gomod2nix.nix.)
    (mkLinterFixtureCheck {
      name = "gomod2nix";
      label = "missing-tool-non-go-pass";
      evalPkgs = builtins.removeAttrs pkgs [ "gomod2nix" ];
      files = {
        "flake.nix" = "{ }\n";
      };
    })
    (mkLinterFixtureCheck {
      name = "gomod2nix";
      label = "missing-tool-go-fail";
      evalPkgs = builtins.removeAttrs pkgs [ "gomod2nix" ];
      files = {
        "go.mod" = "module example.com/foo\n";
      };
      expectFail = true;
      expectToken = "pkgs.gomod2nix is unavailable";
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

  ];

  allFixtures =
    fixtures
    ++ agentsMdWalkFixtures
    ++ gitRemotesFixtures
    ++ deadnixFixtures
    ++ mergeDriverFixtures
    ++ gitMergeDriversFixtures;
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
