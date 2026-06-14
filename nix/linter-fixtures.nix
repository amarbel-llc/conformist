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
  ];
in
builtins.listToAttrs (
  map (d: {
    inherit (d) name;
    value = d;
  }) fixtures
)
// {
  # Aggregate: a link farm that forces every fixture to build. The cheap recipe
  # `just verify-linter-fixtures` builds this one path instead of the full
  # `nix flake check` (which would also build the ~130 registry smoke checks).
  linter-fixtures = pkgs.linkFarmFromDrvs "linter-fixtures" fixtures;
}
