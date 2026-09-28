# The generic codegen-repair lane as a conformist linter (conformist#124).
#
# A generator (tommy, a dagnabit facade export, …) already ships a flake check that
# FAILS when its generated output is stale. igloo#80 makes that check also name
# where its repair lives: the check derivation carries `passthru.codegenPatch`,
# whose `$out/patch` is a diff bringing the tree back in line (empty when already
# current). This linter's repair walks `checks.<system>`, builds every such patch,
# and applies it — so a producer bump restamps its consumers' generated files at
# COMMIT time instead of surfacing as drift at the merge gate.
#
# REPAIR-ONLY. The read-only `command` is a deliberate no-op: the drift check that
# carries the passthru IS the check-mode gate, so running a second copy of it here
# would double-report every finding, pay for a second build, and give two places to
# debug. Only `repair-command` does work. That is also what makes this linter safe
# in the PURE `eng` preset despite needing live nix and git: check mode (the
# sandboxed checks.formatting lane) never invokes the repair.
#
# GENERATOR-AGNOSTIC. Nothing here names a generator, and nothing needs a
# per-consumer flake edit to onboard: a repo that imports presets.eng gets the lane,
# and a repo whose checks carry no codegenPatch gets a no-op. The only host
# requirements are nix and git.
#
# FAIL-SOFT. `conformist codegen-repair` warns and exits 0 when discovery or a
# per-check build fails, because this runs in a git pre-commit hook where a
# non-zero exit blocks the commit — an unreachable nix must not stop a repo from
# committing, and the drift check still catches whatever staleness results. Set
# `strict = true` for a lane that would rather stop.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.codegen-repair;

  # The repair is conformist's own `codegen-repair` subcommand, so the linter needs
  # a conformist to invoke. `package` is nullOr rather than a plain package with a
  # default because nix/checks.nix's registry smoke test evaluates each linter
  # module WITHOUT setting the top-level `package` option (which has no default —
  # conformist is not in nixpkgs) and then deep-forces config.settings; an
  # unconditional `config.package` reference would turn enabling this linter into
  # an eval error there. Null means "resolve conformist from PATH", which is
  # correct inside conformist's own devShell and for the standalone-artifact
  # delivery route; presets/eng.nix wires the real package so an adopting repo gets
  # the hermetic one without touching this option.
  conformist = if cfg.package == null then "conformist" else lib.getExe' cfg.package "conformist";

  strictFlag = lib.optionalString cfg.strict " --strict";

  # A no-op with a NAME, rather than coreutils' `true`: when a run's statistics or
  # a debug log mention this linter, the binary it ran should say why it did
  # nothing.
  noop = pkgs.writeShellApplication {
    name = "conformist-codegen-repair-check";
    text = ''
      # Repair-only by design (conformist#124): the repo's own codegen drift check
      # is the check-mode gate for staleness, so this reports nothing. See
      # nix/linters/codegen-repair.nix.
      exit 0
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-codegen-repair";
    text = ''
      # cwd is the tree root; this whole-tree linter takes no file arguments.
      #
      # Self-gate on a flake: without one there are no checks to discover, and the
      # engine's fail-soft path would otherwise warn on every repair run in a
      # non-flake repo. Staying silent there is the difference between a lane a
      # repo can leave enabled and one it turns off.
      [ -f flake.nix ] || exit 0

      exec ${conformist} codegen-repair${strictFlag}
    '';
  };
in
{
  options.linters.codegen-repair = {
    enable = lib.mkEnableOption ''
      the generic codegen-repair lane: apply every flake check's
      passthru.codegenPatch to the working tree at repair time (conformist#124).
      Repair-only — the repo's own drift checks stay the check-mode gate
    '';

    package = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      defaultText = lib.literalExpression "null (resolve `conformist` from PATH)";
      example = lib.literalExpression "conformist.packages.\${system}.default";
      description = ''
        The conformist package whose `codegen-repair` subcommand performs the
        repair. Null resolves `conformist` from PATH, which is what conformist's
        own devShell and the standalone-artifact delivery route want.

        `conformist.lib.presets.eng` sets this from the module's own
        `package` option, so an adopting repo gets a hermetic store path without
        configuring anything.
      '';
    };

    strict = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Fail the repair when discovery or a per-check patch build fails, instead
        of warning and proceeding. The default is lenient because this lane runs
        inside a git pre-commit hook, where a non-zero exit blocks the commit and
        an unreachable nix would block every commit in the repo; the drift check
        still catches any staleness that results. Turn it on for a lane that would
        rather stop than let a possibly-stale output through.

        A patch that was BUILT but will not apply fails the repair either way — it
        was generated from this very tree, so a refusal is a real inconsistency.
        Both that refusal and this option reach the caller because the linter sets
        `repair-must-succeed` (below), without which conformist discards a
        repair-command's exit status entirely.
      '';
    };

    includes = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [
        "*.go"
        "flake.lock"
        "go.nix"
      ];
      description = ''
        Fire-trigger globs: the whole-tree repair runs when a matched file has
        changed. NOT the input set — the repair reads the flake's checks, not these
        files.

        The default covers what moves a generated file in practice: Go sources (a
        changed type or config struct), and the two lockfiles whose bump is how a
        producer's new output reaches a consumer. A generator that consumes
        something else declares it as `passthru.codegenIncludes` on its check;
        `conformist codegen-repair` logs those so a mismatch is visible, and
        `extra-includes` is where to add them. They cannot be unioned
        automatically: this module is evaluated INSIDE the consumer's own flake, so
        reading `checks` from here to learn them would be an infinite recursion
        through `checks.formatting`.
      '';
    };

    extra-includes = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [
        "*.proto"
        "schema/*.json"
      ];
      description = ''
        Extra fire-trigger globs appended to `includes`, for the inputs a repo's
        own generators consume — typically the `passthru.codegenIncludes` its
        checks declare.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.codegen-repair = {
      command = lib.getExe noop;
      "repair-command" = lib.getExe repair;
      includes = cfg.includes ++ cfg.extra-includes;
      passes-files = false;

      # A codegenPatch rewrites tracked generated files, creates new ones, and
      # deletes relocated ones (igloo#80: patches carry new-file and deleted-file
      # hunks), and the whole point is that those land in the commit that triggered
      # the repair. So all three staging tiers are on: restage the modified
      # (conformist#55), stage the brand-new (conformist#56), stage the deletions
      # (conformist#57). Attribution is per-linter — the --staged/--commit hook
      # takes a git-status delta around THIS linter's repair — so enabling the
      # dangerous tiers here does not widen what any other linter may stage.
      "restage-repair-outputs" = true;
      "stage-new-outputs" = true;
      "stage-deleted-outputs" = true;

      # This lane exists to leave generated files consistent with their sources,
      # so a repair that could NOT do its job must stop the commit or merge it is
      # gating rather than let it through silently. conformist otherwise discards a
      # repair-command's exit status (most repairs are best-effort — `cargo clippy
      # --fix` deliberately exits non-zero with an unfixable remainder), so this
      # opt-in is what makes `strict` and the engine's own refusal on an
      # unappliable patch actually block. The reason travels in the error, which
      # matters because repair output is logged below the default level.
      "repair-must-succeed" = true;
    };
  };
}
