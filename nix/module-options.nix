# The conformist settings schema plus the
# build.{configFile,wrapper,preCommit,repair,check,programs} outputs. preCommit
# (issue #47) and repair (issue #54) are conformist additions: store-pinned hook
# commands (`conformist --staged --exit-zero-on-fix` and `conformist --commit
# --amend --exit-zero-on-fix`, sharing one wrapper body via mkHookWrapper), so a
# consumer can drive git pre-commit / spinclass repair hooks from the module
# instead of a hand-written PATH-relative config.
# Ported from treefmt-nix's module-options.nix with these adaptations
# (see nix/default.nix and issue #4 for rationale):
#   - the wrapped/checked binary is `conformist`, not `treefmt`;
#   - `package` has NO default (conformist is not in nixpkgs) — the consumer MUST
#     set it;
#   - the settings schema carries a `linter` table parallel to `formatter`
#     (RFC 0001 §4);
#   - `build.check` runs `conformist check` and trusts its 0/1/2 exit code
#     (RFC 0001 §7) instead of treefmt-nix's cp + git-diff dance, and passes an
#     explicit `--tree-root` (the `/nix/store` config-file path would otherwise
#     make conformist default tree-root to /nix/store — see issue #2);
#   - the wrapper always uses `--tree-root-file` (conformist forks treefmt v2.5.0,
#     which always supports it), dropping treefmt-nix's version-compare branch;
#   - `build.programs` unions formatters and linters so the devShell gets both;
#   - the per-tool `meta` apparatus is reduced to a no-op freeform option so the
#     ported program modules' `meta.maintainers = [...]` lines stay valid.
{
  config,
  options,
  lib,
  pkgs,
  ...
}:
let
  inherit (lib) mkOption types;

  # A new kind of option type that calls lib.getExe on derivations.
  exeType = lib.mkOptionType {
    name = "exe";
    description = "Path to executable";
    check = x: lib.isString x || builtins.isPath x || lib.isDerivation x;
    merge =
      loc: defs:
      let
        res = lib.mergeOneOption loc defs;
      in
      if lib.isString res || builtins.isPath res then "${res}" else lib.getExe res;
  };

  configFormat = pkgs.formats.toml { };

  # Remove keys in the setting that are "empty" to keep the config file lean.
  emptySettingsKeys =
    lib.optional (config.settings.excludes == [ ]) "excludes"
    ++ lib.optional (config.settings.on-unmatched == null) "on-unmatched"
    # Drop an empty formatter/linter table so a single-kind config doesn't emit
    # a bare `[formatter]` / `[linter]` header (conformist#7).
    ++ lib.optional (config.settings.formatter == { }) "formatter"
    ++ lib.optional (config.settings.linter == { }) "linter"
    # Remove deprecated 'global' key (created by mkRenamedOptionModule for
    # backwards compatibility).
    ++ [ "global" ];

  settingsData = builtins.removeAttrs config.settings emptySettingsKeys;

  configFile = configFormat.generate "conformist.toml" settingsData;

  # A tool submodule (shared shape for formatter and linter tables). The
  # freeform TOML type carries any field conformist understands that isn't
  # declared explicitly here (e.g. check-command, sandbox for formatters;
  # repair-command, no-positional-arg-support for both) so the generated config
  # stays forward-compatible with conformist's config struct.
  toolSubmodule = types.submodule [
    {
      freeformType = configFormat.type;
      options = {
        command = mkOption {
          description = "Executable to invoke (formatter repair action / linter check action)";
          type = exeType;
        };

        options = mkOption {
          description = "List of arguments to pass to the command";
          type = types.listOf types.str;
          default = [ ];
        };

        includes = mkOption {
          description = "List of files to include. Supports globbing.";
          type = types.listOf types.str;
        };

        excludes = mkOption {
          description = "List of files to exclude. Supports globbing. Takes precedence over includes.";
          type = types.listOf types.str;
          default = [ ];
        };
      };
    }
  ];

  # The schema of the conformist config data structure.
  configSchema = mkOption {
    default = { };
    description = "The contents of conformist.toml (treefmt-era filename / TOML shape)";
    type = types.submodule {
      imports = [
        (lib.mkRenamedOptionModule [ "global" "excludes" ] [ "excludes" ])
        (lib.mkRenamedOptionModule [ "global" "on-unmatched" ] [ "on-unmatched" ])
      ];
      freeformType = configFormat.type;
      options = {
        excludes = mkOption {
          description = "A global list of paths to exclude. Supports glob.";
          type = types.listOf types.str;
          default = [ ];
          example = [ "node_modules/*" ];
        };

        on-unmatched = mkOption {
          description = "Log paths that did not match any formatters at the specified log level.";
          type = types.nullOr (
            types.enum [
              "debug"
              "info"
              "warn"
              "error"
              "fatal"
            ]
          );
          default = null;
        };

        formatter = mkOption {
          type = types.attrsOf toolSubmodule;
          default = { };
          description = "Set of formatters to use";
        };

        linter = mkOption {
          type = types.attrsOf toolSubmodule;
          default = { };
          description = "Set of linters to use (RFC 0001 §4)";
        };
      };
      config = {
        excludes = lib.mkIf config.enableDefaultExcludes [
          # generated lock files i.e. yarn, cargo, nix flakes
          "*.lock"
          # Files generated by patch
          "*.patch"

          # NPM
          "package-lock.json"

          # Go
          # In theory go mod tidy could format this, but it has other
          # side-effects beyond formatting.
          "go.mod"
          "go.sum"

          # VCS
          ".gitattributes"
          ".gitignore"
          ".gitmodules"
          ".hgignore"
          ".svnignore"

          # License
          "LICENSE"
        ];
      };
    };
  };

  # Shared body for the store-pinned git-hook wrappers (build.preCommit and
  # build.repair, conformist#47/#51/#54). Both run the consumer's pinned
  # conformist over the LIVE worktree with the store config; they differ only in
  # the mode flags (`--staged` restage vs `--commit --amend` repair) and the
  # installed name. Deriving both from one definition is why #54 asked for a
  # factored body instead of a second hand-rolled wrapper that drifts from the
  # first. Like build.wrapper, the hook locates the live worktree via
  # `--tree-root-file` (NOT a store --tree-root): the hook runs in the author's
  # checkout at commit time, where these modes write to the index/worktree of
  # that checkout. `unset PRJ_ROOT` keeps direnv's PRJ_ROOT from overriding the
  # baked --tree-root-file. `--exit-zero-on-fix` maps exit-3 (fixes applied) to
  # 0 so a `nonzero = abort` hook treats a successful repair as success
  # (conformist#35/#39).
  mkHookWrapper =
    {
      name,
      modeFlags,
    }:
    let
      flagLines = lib.concatMapStringsSep "\n" (f: "  ${f} \\") (modeFlags ++ [ "--exit-zero-on-fix" ]);
      code = ''
        set -euo pipefail
        unset PRJ_ROOT
        exec ${config.package}/bin/conformist \
        ${flagLines}
          --config-file=${config.build.configFile} \
          --tree-root-file=${config.projectRootFile} \
          "$@"
      '';
      x = pkgs.writeShellScriptBin name code;
    in
    x.overrideAttrs (prev: {
      meta = config.package.meta // prev.meta;
    });

  # Accumulate the enabled tool packages from both the formatter (programs.*)
  # and linter (linters.*) namespaces, for the devShell.
  enabledPackages =
    cfgNamespace: optNamespace:
    pkgs.lib.concatMapAttrs (
      k: v:
      if (optNamespace.${k}.enable.visible or true) && v.enable then
        {
          "${k}" = if optNamespace.${k}.finalPackage.isDefined then v.finalPackage else v.package;
        }
      else
        { }
    ) cfgNamespace;
in
{
  # Schema
  options = {
    # Represents the conformist config.
    settings = configSchema;

    package = mkOption {
      description = ''
        The conformist package to wrap. conformist is not in nixpkgs, so this has no
        default — the consumer MUST set it (e.g. to the conformist flake's package
        output, or its own locally-built derivation).
      '';
      type = types.package;
    };

    projectRootFile = mkOption {
      description = ''
        File to look for to determine the root of the project in the
        build.wrapper.
      '';
      default = "flake.nix";
      type = types.str;
    };

    enableDefaultExcludes = mkOption {
      description = ''
        Enable the default excludes in the conformist configuration.
      '';
      type = types.bool;
      default = true;
    };

    # A reduced, no-op meta surface. The ported program modules carry
    # `meta.maintainers = [ ... ]`; declaring a freeform meta lets them port
    # verbatim without stripping those lines. treefmt-nix's platform-filtering
    # apparatus (broken/platforms/brokenPlatforms/skipExample) is intentionally
    # dropped — conformist targets the standard systems and per-tool brokenness is
    # handled when it bites.
    meta = mkOption {
      type = types.submodule { freeformType = (pkgs.formats.json { }).type; };
      internal = true;
      default = { };
      description = "Module metadata (unused; kept so ported modules' meta.* stays valid).";
    };

    # Outputs
    build = {
      devShell = mkOption {
        description = "The development shell with conformist and its underlying programs";
        type = types.package;
        readOnly = true;
      };
      configFile = mkOption {
        description = ''
          Contains the generated config file derived from the settings.
        '';
        type = types.path;
      };
      wrapper = mkOption {
        description = ''
          The conformist package, wrapped with the config file. Runs in repair
          mode (`nix fmt`).
        '';
        type = types.package;
        defaultText = lib.literalMD "wrapped `conformist` command";
        default =
          let
            code = ''
              set -euo pipefail
              unset PRJ_ROOT
              exec ${config.package}/bin/conformist \
                --config-file=${config.build.configFile} \
                --tree-root-file=${config.projectRootFile} \
                "$@"
            '';
            x = pkgs.writeShellScriptBin "conformist" code;
          in
          x.overrideAttrs (prev: {
            meta = config.package.meta // prev.meta;
          });
      };
      preCommit = mkOption {
        description = ''
          A git pre-commit hook command: the conformist package wrapped with the
          generated config file, run as `conformist --staged --exit-zero-on-fix`.
          It formats only the index-staged files and restages the formatted
          content (lint-staged semantics, conformist#25/#40), creating no commit,
          and exits 0 even when it applied fixes so a `nonzero = abort` hook
          treats a successful repair as success (conformist#35/#39).

          This is the supported way to drive a pre-commit hook FROM the module
          (conformist#47): the config (and therefore every formatter/linter's
          command) is pinned to the store at build time and rebuilds when inputs
          change, so the hook needs no hand-written, PATH-relative
          `conformist.toml`. Wire it into a sweatfile (or other hook runner) by
          its installed name — it is placed on the devShell PATH as
          `conformist-pre-commit` — e.g. `pre-commit = "conformist-pre-commit"`.

          A consumer MUST get this hook from its OWN module eval
          (`<its-eval>.config.build.preCommit`), NOT from a bare
          `pre-commit = "conformist --staged --exit-zero-on-fix"` string and NOT
          from conformist's own `packages.conformist-pre-commit` (which is built
          from conformist's config, runs conformist's formatters on the
          consumer's tree, and is not config-specific). The bare-string form is
          the silent-skip trap of conformist#51: it resolves formatters from
          PATH, so if the author's shell lacks gofumpt/nixfmt/… the staged repair
          quietly skips those file types. This wrapper avoids that because its
          formatter commands are store paths from the consumer's own generated
          config. The `#eng` template wires this output + a sweatfile example
          (templates/eng/) as the reference consumer path.

          Like build.wrapper it locates the live worktree via
          `--tree-root-file=${config.projectRootFile}` (NOT a store --tree-root):
          the hook runs in the author's checkout at commit time, where `--staged`
          needs the real git worktree. The store config is a read-only input;
          --staged only writes to the index/worktree of that checkout.

          Caveat: a linter whose command shells out to an AMBIENT tool (e.g. a
          `go vet ./...` linter needs `go` on PATH) still depends on the dev
          environment at commit time — the store-pinned config makes conformist
          and its directly-named tools hermetic, but it cannot supply a tool the
          linter itself execs by bare name. Such linters behave exactly as they
          do under a hand-written config.
        '';
        type = types.package;
        defaultText = lib.literalMD "conformist pre-commit hook command";
        default = mkHookWrapper {
          name = "conformist-pre-commit";
          modeFlags = [ "--staged" ];
        };
      };
      repair = mkOption {
        description = ''
          A repair hook command: the conformist package wrapped with the
          generated config file, run as
          `conformist --commit --amend --exit-zero-on-fix`. It is the
          `--commit --amend` sibling of build.preCommit (conformist#54) —
          everything in build.preCommit's contract applies, with one mode
          difference: instead of restaging staged files (`--staged`), it
          repairs the worktree and folds the run's fixes into HEAD via
          `git commit --amend --no-edit` (conformist#24/#33), exiting 0 even
          when it amended so a `nonzero = abort` hook treats a successful
          repair as success (conformist#35/#39).

          This is the supported way to drive a spinclass pre-merge REPAIR hook
          (FDR 0018) FROM the module: name it in a sweatfile by its installed
          name — it is placed on the devShell PATH as `conformist-repair` —
          e.g. `repair = "conformist-repair"`. Consumers MUST get it from their
          OWN module eval (`<its-eval>.config.build.repair`), NOT from a bare
          `repair = "conformist --commit --amend --exit-zero-on-fix"` string,
          which is the silent-skip trap of conformist#51 (it resolves
          formatters from PATH, so a shell missing gofumpt/nixfmt/… quietly
          skips those file types). This wrapper avoids that because its
          formatter commands are store paths from the consumer's own generated
          config — exactly as build.preCommit does, sharing one definition so
          the two can't drift.

          Like build.preCommit it requires the devShell active (the wrapper is
          on the devShell PATH) and depends on the dev environment for any
          linter that execs an ambient tool by bare name. Additionally, because
          `--amend` re-signs HEAD, a locked commit-signing agent fails the
          amend rather than producing an unsigned commit.
        '';
        type = types.package;
        defaultText = lib.literalMD "conformist repair hook command";
        default = mkHookWrapper {
          name = "conformist-repair";
          modeFlags = [
            "--commit"
            "--amend"
          ];
        };
      };
      programs = mkOption {
        type = types.attrsOf types.package;
        description = ''
          Attrset of formatter and linter programs enabled in the conformist
          configuration. The key is the tool name; the value is the package used
          to run it.
        '';
        defaultText = lib.literalMD "Programs used in configuration";
        default =
          (enabledPackages config.programs options.programs)
          // (enabledPackages config.linters options.linters);
      };
      check = mkOption {
        description = ''
          Create a flake check that the given project tree passes
          `conformist check` (formatters would make no change and linters report no
          findings) without modifying anything.

          Input argument is the path to the project tree (usually 'self').
        '';
        type = types.functionTo types.package;
        defaultText = lib.literalMD "Default check implementation";
        default =
          self:
          pkgs.runCommandLocal "conformist-check"
            {
              # Invoke the RAW conformist binary, NOT build.wrapper: the wrapper
              # hardcodes --tree-root-file (for repair-mode `nix fmt`), which is
              # mutually exclusive with the --tree-root we must pass here
              # (cmd/root.go MarkFlagsMutuallyExclusive). Setting both errors.
              meta.description = "Check that the project tree passes conformist";
            }
            ''
              set -e
              # conformist check is strictly read-only (RFC 0001 §2): it never
              # writes inside the tree root, so we point it straight at the
              # (read-only) source store path. --tree-root MUST be explicit —
              # otherwise conformist would fall back to the config-file's
              # directory (/nix/store) and walk the entire store (issue #2).
              # Exit code 0 = clean, 1 = findings, 2 = operational error
              # (RFC 0001 §7); any non-zero fails the build. We do NOT pass
              # --quiet so findings/errors land in the build log.
              ${config.package}/bin/conformist --version
              ${config.package}/bin/conformist check \
                --config-file=${config.build.configFile} \
                --tree-root=${self} \
                ${self}
              touch $out
            '';
      };
    };
  };

  # Config
  config.build = {
    inherit configFile;
    devShell = pkgs.mkShell {
      nativeBuildInputs = [
        config.build.wrapper
        config.build.preCommit
        config.build.repair
      ]
      ++ (lib.attrValues config.build.programs);
    };
  };
}
