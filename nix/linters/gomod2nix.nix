# gomod2nix.toml drift as a conformist whole-tree linter.
#
# gomod2nix.toml is generated from go.mod/go.sum by the gomod2nix CLI and must
# stay in sync with the module graph; nothing otherwise gates its drift (the same
# class of bug that shipped a stale godyn-graph.json). This linter mechanizes the
# check: the read-only `command` reports a gomod2nix.toml that has drifted from
# go.mod/go.sum; the `repair-command` (repair mode / `nix fmt`) regenerates it and
# stages the result.
#
# The gomod2nix CLI has NO native check/verify mode — its only flags are
# --dir/--outdir/--jobs/--with-deps and it always writes the file (upstream
# internal/cmd/root.go). So the check regenerates into a temp dir (--outdir) and
# diffs against the committed copy, mirroring the old verify-godyn-graph shape. A
# native check mode is requested upstream (amarbel-llc/gomod2nix#14); swap the
# check to it once available.
#
# Whole-tree check (passes-files=false): runs once at the tree root. Uses
# writeShellApplication so the scripts' shebangs are patched (cf. conformist#19).
# Lives in the IMPURE self-check lane (nix/conformist-impure.nix): the check runs
# gomod2nix, which needs the real go.mod/go.sum + Go module resolution (network /
# module cache, not a read-only /nix/store copy), and the repair runs `git add` —
# both require the live working tree, like agents-md.
#
# No nix/linter-fixtures.nix entry: that harness runs each check in a pure
# `runCommandLocal` sandbox (no network, no module cache), but this check shells
# out to gomod2nix, which resolves the module graph and so cannot run there. The
# clean-tree pass is exercised by `just lint-worktree` on conformist's own tree;
# drift true-positives are verified manually (see the linter's PR / commit).
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.gomod2nix;

  check = pkgs.writeShellApplication {
    name = "conformist-gomod2nix";
    runtimeInputs = with pkgs; [
      coreutils
      gomod2nix
    ];
    text = ''
      # cwd is the tree root; this whole-tree check takes no file arguments.
      [ -f go.mod ] || {
        echo "gomod2nix: no go.mod at tree root — nothing to check"
        exit 0
      }

      if [ ! -f gomod2nix.toml ]; then
        echo "gomod2nix: gomod2nix.toml is missing; run \`nix fmt\` / repair (or \`just build-gomod2nix\`)" >&2
        exit 1
      fi

      # No native check mode (amarbel-llc/gomod2nix#14): regenerate into a temp
      # dir and diff against the committed copy. --outdir writes <dir>/gomod2nix.toml.
      tmp=$(mktemp -d)
      trap 'rm -rf "$tmp"' EXIT

      gomod2nix --dir . --outdir "$tmp"

      if ! diff -u gomod2nix.toml "$tmp/gomod2nix.toml"; then
        echo "gomod2nix: gomod2nix.toml is stale relative to go.mod/go.sum; run \`nix fmt\` / repair (or \`just build-gomod2nix\`) and commit the result" >&2
        exit 1
      fi

      echo "gomod2nix: gomod2nix.toml is in sync with go.mod/go.sum"
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-gomod2nix-repair";
    runtimeInputs = with pkgs; [
      coreutils
      gomod2nix
      git
    ];
    text = ''
      # cwd is the tree root.
      [ -f go.mod ] || exit 0 # not a Go project — idempotent no-op

      # Regenerate gomod2nix.toml in place from go.mod/go.sum.
      gomod2nix --dir .

      # Self-stage: `conformist --staged` only re-stages files that were ALREADY
      # staged, so a regenerated gomod2nix.toml the author didn't pre-stage would
      # be left as an unstaged working-tree change and the commit would land
      # without it. Staging it here makes the regenerated file land in the commit
      # regardless of what was pre-staged (cf. agents-md's repair). The `|| true`
      # keeps repair working outside a git worktree / when already staged.
      git add gomod2nix.toml 2>/dev/null || true
      echo "gomod2nix: regenerated gomod2nix.toml from go.mod/go.sum"
    '';
  };
in
{
  options.linters.gomod2nix = {
    enable = lib.mkEnableOption "the gomod2nix.toml drift whole-tree check (regenerate + diff; repair regenerates and stages)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.gomod2nix = {
      command = lib.getExe check;
      "repair-command" = lib.getExe repair;
      # Watch the real Go module files. go.mod/go.sum are in conformist's GLOBAL
      # excludes (module-options.nix default-excludes them so formatters never
      # rewrite them), and globally-excluded files are normally dropped before
      # per-linter matching — so ignore-global-excludes (conformist#44) is what
      # lets this whole-tree check trigger when go.mod/go.sum change. The matched
      # files are only a trigger; the script reads the real go.mod itself.
      includes = [
        "go.mod"
        "go.sum"
        "gomod2nix.toml"
      ];
      ignore-global-excludes = true;
      passes-files = false;
    };
  };
}
