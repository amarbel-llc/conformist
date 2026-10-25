# amarbel-llc convention: the default branch is `master`; there is no `main`
# branch. Whole-tree check (passes-files=false): reads git state, no file args.
#
# IMPURE: needs a live .git (local + remote-tracking refs), so it runs only via
# the non-sandbox `just lint-worktree` lane, not the sandboxed checks.formatting.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.git-default-branch;

  check = pkgs.writeShellApplication {
    name = "conformist-git-default-branch";
    runtimeInputs = with pkgs; [
      gitMinimal # core subcommands only; full git's closure drags in python3 (#62)
      coreutils
      gnugrep
    ];
    text = ''
      # No `main` branch may exist (local, remote-tracking, or any remote).
      main_refs=$(git for-each-ref --format='%(refname:short)' refs/heads refs/remotes 2>/dev/null | grep -xE 'main|[^/]+/main' || true)
      if [ -n "$main_refs" ]; then
        printf 'git-default-branch: a "main" branch exists (%s); this repo uses "master" — delete main\n' "$(echo "$main_refs" | tr '\n' ' ')" >&2
        exit 1
      fi

      # When origin's default is known, it must point at master.
      head=$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)
      if [ -n "$head" ] && [ "$head" != "origin/master" ]; then
        echo "git-default-branch: origin default is \"$head\", expected origin/master" >&2
        exit 1
      fi

      echo "git-default-branch: default is master; no main branch"
    '';
  };
in
{
  options.linters.git-default-branch = {
    enable = lib.mkEnableOption "the default-branch-is-master / no-main whole-tree check";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.git-default-branch = {
      command = lib.getExe check;
      includes = [ "flake.nix" ];
      passes-files = false;
    };
  };
}
