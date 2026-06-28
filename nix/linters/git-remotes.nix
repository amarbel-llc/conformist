# and-so-can-you #8: every git remote MUST use SSH (git@.../ssh://), not a
# network transport that needs separate credentials — https://, http://, git://,
# ftp:// all cause auth failures or insecure fetches on push/pull. Local path /
# file:// remotes are left alone (no auth concern). Whole-tree check
# (passes-files=false): reads git state via `git remote -v`, takes no file args.
#
# The read-only `command` reports any non-SSH remote (any host); the
# `repair-command` (repair mode / `nix fmt`) rewrites the subset with an
# unambiguous SSH form — github.com https://, http://, git:// remotes — to
# git@github.com:OWNER/REPO.git via `git remote set-url`, iterating the remotes
# the check flags (conformist#68). Non-github hosts, local-path, and file://
# remotes have no canonical SSH form, so repair leaves them alone and the check
# keeps reporting them. Idempotent: an already-SSH remote is a no-op.
#
# IMPURE: it needs a live .git, which is NOT present in the sandboxed
# checks.formatting derivation (a /nix/store copy of tracked files). It runs only
# via the non-sandbox `just check-worktree` lane (the conformist-impure config),
# against the working tree. Do not enable it in nix/conformist.nix.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.git-remotes;

  check = pkgs.writeShellApplication {
    name = "conformist-git-remotes";
    runtimeInputs = with pkgs; [
      git
      gawk
      coreutils
      gnused
    ];
    text = ''
      bad=$(git remote -v | awk '$2 ~ /^(https?|git|ftp):\/\// {print $1"\t"$2}' | sort -u)
      if [ -n "$bad" ]; then
        echo "git-remotes(#8): non-SSH remote URL(s) found — use SSH (git@github.com:... or ssh://):" >&2
        printf '%s\n' "$bad" | sed 's/^/  /' >&2
        exit 1
      fi
      echo "git-remotes(#8): all remotes use SSH"
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-git-remotes-repair";
    runtimeInputs = with pkgs; [
      git
      coreutils
    ];
    text = ''
      # cwd is the tree root; this whole-tree repair takes no file arguments.
      # Map a github.com network-transport URL (https://, http://, git://) to its
      # unambiguous SSH form; return non-zero for anything else (non-github host,
      # local path, file://, already-SSH) so the caller leaves it untouched.
      to_ssh() {
        case "$1" in
          https://github.com/* | http://github.com/* | git://github.com/*) ;;
          *) return 1 ;;
        esac
        path=''${1#*://github.com/} # OWNER/REPO[.git][/]
        path=''${path%/}            # strip a trailing slash
        path=''${path%.git}         # strip a trailing .git
        printf 'git@github.com:%s.git' "$path"
      }

      rewrote=0
      while IFS= read -r name; do
        [ -n "$name" ] || continue

        url=$(git remote get-url "$name")
        if ssh=$(to_ssh "$url") && [ "$ssh" != "$url" ]; then
          git remote set-url "$name" "$ssh"
          echo "git-remotes(#8): rewrote remote '$name' $url -> $ssh"
          rewrote=1
        fi

        # A separately-configured push URL (remote.<name>.pushurl) is rewritten
        # too; without an explicit pushurl, `set-url` above already covers push.
        if git config --get-all "remote.$name.pushurl" >/dev/null 2>&1; then
          purl=$(git remote get-url --push "$name")
          if pssh=$(to_ssh "$purl") && [ "$pssh" != "$purl" ]; then
            git remote set-url --push "$name" "$pssh"
            echo "git-remotes(#8): rewrote push URL of remote '$name' $purl -> $pssh"
            rewrote=1
          fi
        fi
      done < <(git remote)

      if [ "$rewrote" -eq 0 ]; then
        echo "git-remotes(#8): no github.com non-SSH remotes to rewrite"
      fi
    '';
  };
in
{
  options.linters.git-remotes = {
    enable = lib.mkEnableOption "the git-remotes SSH-only whole-tree check (repair rewrites github.com https/http/git remotes to SSH; needs a live .git; non-sandbox lane only)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.git-remotes = {
      command = lib.getExe check;
      "repair-command" = lib.getExe repair;
      # Gate on a file that is always present and walked, so the check fires once
      # for the tree. The check itself ignores files (passes-files=false).
      includes = [ "flake.nix" ];
      passes-files = false;
    };
  };
}
