# CLAUDE.md -> AGENTS.md migration as a conformist whole-tree linter (conformist#18).
#
# The agent-orientation file is converging on AGENTS.md (the cross-tool standard)
# and away from the Claude-specific CLAUDE.md. This linter mechanizes the rename:
# the read-only `command` reports a repo that still needs migrating; the
# `repair-command` (repair mode / `nix fmt`) does `git mv CLAUDE.md AGENTS.md` and
# leaves a relative CLAUDE.md -> AGENTS.md back-compat symlink. Idempotent and
# conflict-safe (never clobbers a divergent AGENTS.md).
#
# The check also enforces a character-count budget on AGENTS.md itself
# (configurable via `max-chars`): an orientation doc that grows unbounded stops
# being something an agent actually reads. There is no repair for this —
# trimming prose is a judgment call, not a mechanical fix.
#
# Whole-tree check (passes-files=false): runs once at the tree root, gated on a
# CLAUDE.md being present. Uses writeShellApplication so the scripts' shebangs are
# patched for the sandbox (cf. conformist#19). Lives in the impure self-check lane
# (nix/conformist-impure.nix): repair needs a live .git, and the check must see
# the real CLAUDE.md symlink in the working tree, not a /nix/store source copy.
#
# The nested-CLAUDE.md walk is scoped to git-TRACKED files (`git ls-files`)
# when a live worktree is present, falling back to a plain `find` outside one
# — so a gitignored child checkout (e.g. a monorepo's vendored `repos/**`)
# never surfaces a finding. `exclude-paths` additionally opts a
# legitimately-named payload (a deployed dotfile whose filename IS the
# product) out of the walk entirely (conformist#95).
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.agents-md;

  check = pkgs.writeShellApplication {
    name = "conformist-agents-md";
    runtimeInputs = with pkgs; [
      coreutils
      findutils
      git
    ];
    text = ''
      # cwd is the tree root; this whole-tree check takes no file arguments.
      findings=0

      excludePaths=(${lib.escapeShellArgs cfg.exclude-paths})

      # $1 matches an exclude-paths entry (glob, repo-relative) — deliberately
      # unquoted in the case pattern so each entry can glob (conformist#95).
      is_excluded() {
        local f="$1" pat
        for pat in "''${excludePaths[@]}"; do
          # shellcheck disable=SC2254
          case "$f" in
            $pat) return 0 ;;
          esac
        done
        return 1
      }

      if [ -L CLAUDE.md ]; then
        target=$(readlink CLAUDE.md)
        if [ "$target" != "AGENTS.md" ]; then
          echo "agents-md: CLAUDE.md is a symlink to '$target', expected AGENTS.md" >&2
          findings=1
        elif [ ! -e AGENTS.md ]; then
          echo "agents-md: CLAUDE.md -> AGENTS.md but AGENTS.md is missing (broken symlink)" >&2
          findings=1
        fi
      elif [ -f CLAUDE.md ]; then
        if [ -e AGENTS.md ]; then
          echo "agents-md: CLAUDE.md and AGENTS.md both exist as regular files; resolve by hand (they may have diverged)" >&2
        else
          echo "agents-md: CLAUDE.md should be migrated to AGENTS.md with a CLAUDE.md -> AGENTS.md symlink (run \`nix fmt\` / repair)" >&2
        fi
        findings=1
      fi

      # Nested CLAUDE.md regular files are reported, not auto-migrated (#18 v1).
      # Scoped to git-TRACKED files when inside a live worktree — this lane is
      # impure and already requires one (conformist-impure.nix) — so a
      # gitignored child checkout (e.g. eng's vendored `repos/**`) never
      # surfaces here; a plain-find fallback covers the rare non-git tree.
      # `exclude-paths` lets a legitimately-named payload (a deployed dotfile
      # whose filename IS the product, e.g. rcm/claude/CLAUDE.md) opt out
      # entirely (conformist#95).
      if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        nested=$(git ls-files | grep -E '(^|/)CLAUDE\.md$' || true)
      else
        nested=$(find . \( -type f -o -type l \) -name CLAUDE.md -not -path './.git/*' | sed 's|^\./||')
      fi

      while IFS= read -r f; do
        [ -n "$f" ] || continue
        if [ "$f" = "CLAUDE.md" ]; then
          continue # root path already handled above (regular file or symlink)
        fi
        if is_excluded "$f"; then
          continue
        fi
        # An ALREADY-migrated nested directory carries exactly the shape this
        # linter's own repair-command produces: AGENTS.md plus a back-compat
        # CLAUDE.md -> AGENTS.md symlink. `git ls-files` lists that tracked
        # symlink (mode 120000), where the pre-#95 `find -type f` walk
        # implicitly skipped it — so without the root branch's symlink handling
        # mirrored here, every migrated subdirectory reports a permanent,
        # unclearable finding (conformist#111).
        if [ -L "$f" ]; then
          target=$(readlink "$f")
          if [ "$target" != "AGENTS.md" ]; then
            echo "agents-md: nested $f is a symlink to '$target', expected AGENTS.md" >&2
            findings=1
          elif [ ! -e "$(dirname "$f")/AGENTS.md" ]; then
            echo "agents-md: nested $f -> AGENTS.md but AGENTS.md is missing (broken symlink)" >&2
            findings=1
          fi
          continue
        fi
        echo "agents-md: nested $f should be migrated to AGENTS.md by hand" >&2
        findings=1
      done <<< "$nested"

      # Size budget: an orientation doc that grows unbounded stops being
      # something an agent actually reads. No repair — trimming prose is a
      # judgment call, not a mechanical fix.
      if [ -f AGENTS.md ]; then
        chars=$(wc -c <AGENTS.md)
        if [ "$chars" -gt ${toString cfg.max-chars} ]; then
          echo "agents-md: AGENTS.md is $chars characters, exceeds the ${toString cfg.max-chars}-character limit" >&2
          findings=1
        fi
      fi

      if [ "$findings" -ne 0 ]; then
        exit 1
      fi
      echo "agents-md: AGENTS.md convention satisfied"
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-agents-md-repair";
    runtimeInputs = with pkgs; [
      coreutils
      git
    ];
    text = ''
      # Migrate the root CLAUDE.md only; nested files are left for manual handling.
      if [ -L CLAUDE.md ] || [ ! -e CLAUDE.md ]; then
        exit 0 # already a symlink, or nothing to migrate — idempotent
      fi

      # CLAUDE.md is a regular file from here.
      if [ -e AGENTS.md ]; then
        echo "agents-md: cannot migrate — CLAUDE.md and AGENTS.md both exist; resolve by hand" >&2
        exit 1
      fi

      git mv CLAUDE.md AGENTS.md 2>/dev/null || mv CLAUDE.md AGENTS.md
      ln -s AGENTS.md CLAUDE.md
      git add AGENTS.md CLAUDE.md 2>/dev/null || true
      echo "agents-md: migrated CLAUDE.md -> AGENTS.md (CLAUDE.md is now a symlink)"
    '';
  };
in
{
  options.linters.agents-md = {
    enable = lib.mkEnableOption "the CLAUDE.md -> AGENTS.md migration whole-tree check (conformist#18)";

    max-chars = lib.mkOption {
      type = lib.types.ints.positive;
      default = 40000;
      description = "Maximum character count for AGENTS.md before the check flags it as too large.";
    };

    exclude-paths = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "rcm/claude/CLAUDE.md" ];
      description = ''
        Repo-relative paths (glob patterns, matched against git-tracked paths)
        to exclude from the nested-CLAUDE.md walk — for a legitimately-named
        payload whose filename IS the product (e.g. a deployed dotfile),
        rather than a convention violation (conformist#95).
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.agents-md = {
      command = lib.getExe check;
      "repair-command" = lib.getExe repair;
      # Gate on any CLAUDE.md (regular file or symlink, root or nested), or on
      # AGENTS.md itself so a size-only edit re-triggers the check; the script
      # itself ignores the file list.
      includes = [
        "CLAUDE.md"
        "**/CLAUDE.md"
        "AGENTS.md"
      ];
      passes-files = false;
    };
  };
}
