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
# BOTH check and repair regenerate FRESH into an empty temp outdir
# (conformist#84): `gomod2nix --dir .` in place reuses the existing toml as an
# incremental cache and silently skips entries whose hash already matches, so an
# in-place repair could "succeed" while the check's fresh regeneration still
# differed. Sharing the one fresh-regen recipe guarantees repair-then-check is
# clean.
#
# `offline = true` (conformist#86) runs the regeneration with GOPROXY=off, so
# module resolution uses only the local Go module cache — for repos whose go.mod
# carries flake-input-bridged modules (the amarbel-llc/nixpkgs RFC 0001
# goFlakeInputs pattern) that are not fetchable from the network. The remainder
# — resolving bridged modules from their flake-input store paths so the check
# works with an empty module cache — is tracked on conformist#86.
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

  # The one shared regeneration recipe (conformist#84): fresh into an empty temp
  # outdir, never in place, so no stale-toml incremental cache can mask drift.
  # Leaves the result at "$tmp/gomod2nix.toml".
  regenSnippet = ''
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT

    ${lib.optionalString cfg.offline ''
      # Network-free resolution from the local Go module cache only
      # (conformist#86): flake-input-bridged modules are not fetchable from
      # the network. A module absent from the cache fails loudly here — see
      # conformist#86 for the pending store-path resolution.
      export GOPROXY=off
    ''}
    gomod2nix --dir . --outdir "$tmp"
  '';

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

      # No native check mode (amarbel-llc/gomod2nix#14): regenerate fresh and
      # diff against the committed copy.
      ${regenSnippet}
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

      # Regenerate FRESH (the same recipe the check uses, conformist#84) and
      # move the result into place. cp (not mv) keeps the existing file's
      # permissions; it also creates a missing gomod2nix.toml.
      ${regenSnippet}
      cp "$tmp/gomod2nix.toml" gomod2nix.toml

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
    offline = lib.mkOption {
      description = ''
        Regenerate with GOPROXY=off: module resolution uses only the local Go
        module cache, never the network. Set this in repos whose go.mod carries
        flake-input-bridged modules (goFlakeInputs) that are not fetchable from
        the network, instead of disabling the linter wholesale. A module absent
        from the local cache fails the check loudly; resolving bridged modules
        from their flake-input store paths is tracked on conformist#86.
      '';
      type = lib.types.bool;
      default = false;
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.gomod2nix = {
      command = lib.getExe check;
      "repair-command" = lib.getExe repair;
      # Watch the real Go module files. go.mod/go.sum are in conformist's GLOBAL
      # excludes (module-options.nix default-excludes them so formatters never
      # rewrite them). A whole-tree check (passes-files=false) is exempt from the
      # global excludes by design — its includes are a trigger gate, not an input
      # set — so it still fires when go.mod/go.sum change (conformist#45, which
      # retired the conformist#44 ignore-global-excludes flag). The matched files
      # are only a trigger; the script reads the real go.mod itself.
      includes = [
        "go.mod"
        "go.sum"
        "gomod2nix.toml"
      ];
      passes-files = false;
    };
  };
}
