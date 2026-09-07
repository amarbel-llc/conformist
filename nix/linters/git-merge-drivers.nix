# conformist-git(7) MERGE DRIVERS: a repo whose generated files stall rebases
# must bind those paths to the conformist merge drivers in .gitattributes.
#
# .gitattributes ALONE does not activate a driver — the `merge.<name>.driver`
# side is registered once per machine in eng's home-manager git config, so the
# binaries are on PATH for every clone. This linter owns only the per-repo half:
# the .gitattributes lines. A repo that ships the lines before the machine has
# the registration is harmless (git falls back to its normal 3-way merge for
# an unknown driver name), so the two halves can roll out in either order.
#
# Whole-tree check (passes-files=false), reads only committed files, so it runs
# in the sandboxed checks.formatting lane as well as `nix fmt`.
#
# DEFAULTS ARE DELIBERATELY NARROW. Only `flake.lock` is bound out of the box,
# because every eng repo has one and the driver's behaviour there is uniform. A
# repo's GENERATED SOURCE globs are repo-specific — conformist cannot know
# which paths a given repo generates, and a wrong glob would route hand-written
# source through a driver that resolves stamp conflicts silently. So codegen
# patterns are opt-in per repo:
#
#     linters.git-merge-drivers.entries = [
#       { pattern = "*_tommy.go"; driver = "conformist-codegen-header"; }
#     ];
#
# See conformist-git(7) and nix/merge-drivers.nix.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.git-merge-drivers;

  entryType = lib.types.submodule {
    options = {
      pattern = lib.mkOption {
        type = lib.types.str;
        description = "The .gitattributes path pattern, matched as the line's first field.";
        example = "*_tommy.go";
      };
      driver = lib.mkOption {
        type = lib.types.str;
        description = "The git merge driver name the pattern must be bound to.";
        example = "conformist-codegen-header";
      };
      when-file = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        description = ''
          Only require this binding when the named file exists at the tree root.
          Keeps the default flake.lock entry silent in a non-flake repo. Null
          means always require it.
        '';
        example = "flake.nix";
      };
    };
  };

  # TSV rather than a shell array so the generated script needs no `''${...}`
  # escaping and the data stays readable in the store.
  entriesFile = pkgs.writeText "git-merge-drivers-entries.tsv" (
    lib.concatMapStrings (
      e: "${e.pattern}\t${e.driver}\t${if e.when-file == null then "" else e.when-file}\n"
    ) cfg.entries
  );

  # Shared by check and repair: emit the missing entries, one TSV line each.
  # A binding is present when some .gitattributes line's FIRST field equals the
  # pattern and one of its attributes is exactly `merge=<driver>`.
  missingScan = ''
    missing=$(
      while IFS="$(printf '\t')" read -r pat drv whenfile; do
        [ -n "$pat" ] || continue
        if [ -n "$whenfile" ] && [ ! -e "$whenfile" ]; then
          continue
        fi
        if [ -f .gitattributes ] && awk -v pat="$pat" -v drv="$drv" '
              /^[[:space:]]*#/ { next }
              NF == 0 { next }
              $1 != pat { next }
              { for (i = 2; i <= NF; i++) if ($i == "merge=" drv) found = 1 }
              END { exit found ? 0 : 1 }
            ' .gitattributes; then
          continue
        fi
        printf '%s\t%s\n' "$pat" "$drv"
      done <${entriesFile}
    )
  '';

  check = pkgs.writeShellApplication {
    name = "conformist-git-merge-drivers";
    runtimeInputs = with pkgs; [
      coreutils
      gawk
    ];
    text = ''
      ${missingScan}

      if [ -z "$missing" ]; then
        echo "conformist-git(7) MERGE DRIVERS: .gitattributes binds every required path"
        exit 0
      fi

      echo "conformist-git(7) MERGE DRIVERS: .gitattributes is missing merge driver bindings:" >&2
      while IFS="$(printf '\t')" read -r pat drv; do
        [ -n "$pat" ] || continue
        echo "  $pat merge=$drv" >&2
      done <<<"$missing"
      echo "run conformist in repair mode to add them" >&2
      exit 1
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-git-merge-drivers-repair";
    runtimeInputs = with pkgs; [
      coreutils
      gawk
      gnugrep
    ];
    text = ''
      ${missingScan}

      if [ -z "$missing" ]; then
        echo "conformist-git(7) MERGE DRIVERS: nothing to add"
        exit 0
      fi

      # Append only. An existing .gitattributes may carry unrelated rules (text
      # normalization, linguist hints, LFS filters) that must survive untouched,
      # and a pattern may already be bound to a DIFFERENT driver — appending
      # leaves that line visible rather than silently rewriting it.
      if [ -s .gitattributes ] && [ -n "$(tail -c 1 .gitattributes)" ]; then
        printf '\n' >>.gitattributes
      fi
      if ! grep -qF 'conformist-git(7)' .gitattributes 2>/dev/null; then
        {
          printf '# conformist-git(7) MERGE DRIVERS: regenerate-on-conflict merge for\n'
          printf '# generated files. The merge.<name>.driver side is registered per-machine\n'
          printf '# in eng home-manager git config; these lines are the per-repo half.\n'
        } >>.gitattributes
      fi

      while IFS="$(printf '\t')" read -r pat drv; do
        [ -n "$pat" ] || continue
        printf '%s merge=%s\n' "$pat" "$drv" >>.gitattributes
        echo "conformist-git(7) MERGE DRIVERS: added '$pat merge=$drv' to .gitattributes"
      done <<<"$missing"
    '';
  };
in
{
  options.linters.git-merge-drivers = {
    enable = lib.mkEnableOption "the .gitattributes merge-driver binding check (conformist-git(7) MERGE DRIVERS)";

    entries = lib.mkOption {
      type = lib.types.listOf entryType;
      default = [
        {
          pattern = "flake.lock";
          driver = "conformist-flake-lock";
          when-file = "flake.nix";
        }
      ];
      description = ''
        The pattern-to-driver bindings .gitattributes must declare. The default
        covers flake.lock only; add this repo's generated-source globs
        explicitly (see the module header for why they are not defaulted).
      '';
    };

  };

  config = lib.mkIf cfg.enable {
    settings.linter.git-merge-drivers = {
      command = lib.getExe check;
      repair-command = lib.getExe repair;
      # Trigger gate for the whole-tree check. Every eng repo has a flake.nix,
      # so this always fires; .gitattributes is listed too so a non-flake repo
      # that already has one is still checked. Not an option: nothing needs to
      # override it, and a whole-tree check is exempt from the global excludes
      # (conformist#45) which would otherwise drop both of these.
      includes = [
        "flake.nix"
        ".gitattributes"
      ];
      passes-files = false;
    };
  };
}
