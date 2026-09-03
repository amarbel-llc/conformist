# and-so-can-you #8: every git remote MUST use SSH (git@.../ssh://), not a
# network transport that needs separate credentials — https://, http://, git://,
# ftp:// all cause auth failures or insecure fetches on push/pull. Local path /
# file:// remotes are left alone (no auth concern). Whole-tree check
# (passes-files=false): reads git state via `git remote -v`, takes no file args.
#
# The read-only `command` runs two INDEPENDENT checks:
#
#   1. Transport: any remote (any name, any host) that isn't SSH is flagged.
#   2. Host: `origin` specifically (not other remotes — a codeberg upstream, a
#      vendored fork, etc. may legitimately point elsewhere) must target
#      `canonical-host` (default "code.linenisgreat.com", the forge) or one of
#      `allowed-hosts` (default [ ], a per-repo escape hatch for a repo
#      deliberately still elsewhere, e.g. circus on GitHub pending its own
#      migration). A repo can fail for either or both reasons; declaring a host
#      in `allowed-hosts` only widens the host check — it does NOT exempt
#      origin from the transport rule, and repair still normalizes it.
#
# The `repair-command` (repair mode / `nix fmt`) rewrites the subset with an
# unambiguous SSH form — github.com and code.linenisgreat.com https://,
# http://, git:// remotes — to their SSH form via `git remote set-url`,
# iterating the remotes the check flags (conformist#68, extended for the
# GitHub->forge migration). github.com keeps its OWNER/REPO path
# (git@github.com:OWNER/REPO.git); code.linenisgreat.com is flat/owner-less
# (git@code.linenisgreat.com:REPO.git). This repair rewrite runs regardless of
# `canonical-host`/`allowed-hosts` — an allowlisted GitHub repo still gets its
# transport normalized, it just isn't required to be on the forge. Any other
# host, local-path, and file:// remotes have no canonical SSH form, so repair
# leaves them alone and the check keeps reporting them (transport and/or host).
# Idempotent: an already-SSH remote on an accepted host is a no-op.
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
      findings=0

      # 1. Transport: every remote (any name, any host) must be SSH.
      #
      # Read the CONFIGURED url, never the effective one. BOTH `git remote -v`
      # and `git remote get-url` apply `url.<base>.insteadOf` rewriting, so a
      # per-machine ssh->https rewrite — e.g. a session using ephemeral push
      # credentials — made this rule flag a repo whose committed remotes are
      # entirely correct, naming a URL the operator cannot find anywhere in
      # .git/config. insteadOf is local policy that never leaves the operator's
      # machine; this rule is about what the repo DECLARES, which is what a
      # reviewer sees and what other clones get. `git config --get` is the only
      # read that sees it (verified by `just debug-git-insteadof`).
      #
      # pushurl is read too: a remote may declare a separate push URL, and the
      # old `git remote -v` scan covered the (push) line.
      bad=$(
        git remote | while IFS= read -r name; do
          [ -n "$name" ] || continue
          for key in url pushurl; do
            # `git config --get-all` exits 1 when the key is unset, which is the
            # NORMAL case for pushurl. Under writeShellApplication's `set -euo
            # pipefail` that would abort the whole check before it reports
            # anything, so swallow it explicitly.
            { git config --get-all "remote.$name.$key" 2>/dev/null || true; } | while IFS= read -r u; do
              [ -n "$u" ] || continue
              case "$u" in
                http://* | https://* | git://* | ftp://*) printf '%s\t%s\n' "$name" "$u" ;;
              esac
            done
          done
        done | sort -u
      )
      if [ -n "$bad" ]; then
        echo "git-remotes(#8): non-SSH remote URL(s) found — use SSH (git@github.com:... or ssh://):" >&2
        printf '%s\n' "$bad" | sed 's/^/  /' >&2
        findings=1
      fi

      # 2. Host: origin specifically must target the canonical host or one of
      # this repo's declared allowed-hosts. Independent of the transport rule
      # above — a repo can fail for either or both reasons.
      canonical_host=${lib.escapeShellArg cfg.canonical-host}
      allowed_hosts=${lib.escapeShellArg (lib.concatStringsSep " " cfg.allowed-hosts)}

      # Extract the host from an SSH scp-like URL (user@host:path), an
      # ssh://[user@]host[:port]/path URL, or a network-transport
      # scheme://[user@]host[:port]/path URL. Returns non-zero (and prints
      # nothing) for anything else — a local path, a bare file:// path with no
      # host, etc. — so those are silently exempt from the host check, same as
      # they already are from repair's canonicalization.
      origin_host() {
        url=$1
        case "$url" in
          *://*)
            rest=''${url#*://}
            rest=''${rest#*@}
            host=''${rest%%/*}
            host=''${host%%:*}
            ;;
          *@*:*)
            rest=''${url#*@}
            host=''${rest%%:*}
            ;;
          *)
            return 1
            ;;
        esac
        [ -n "$host" ] || return 1
        printf '%s' "$host"
      }

      # Configured, not effective — same insteadOf reasoning as the transport
      # rule above.
      if origin_url=$(git config --get remote.origin.url 2>/dev/null); then
        if host=$(origin_host "$origin_url") && [ "$host" != "$canonical_host" ]; then
          case " $allowed_hosts " in
            *" $host "*) ;; # explicitly allowlisted for this repo
            *)
              echo "git-remotes(#8): origin remote is on '$host', but the canonical host is '$canonical_host'" >&2
              if [ -n "$allowed_hosts" ]; then
                echo "  (this repo's allowed-hosts also permits: $allowed_hosts)" >&2
              fi
              findings=1
              ;;
          esac
        fi
      fi

      if [ "$findings" -ne 0 ]; then
        exit 1
      fi
      echo "git-remotes(#8): all remotes use SSH; origin is on an approved host"
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
      # Map a github.com OR code.linenisgreat.com (the forge) network-transport
      # URL (https://, http://, git://) to its unambiguous SSH form; return
      # non-zero for anything else (unknown host, local path, file://,
      # already-SSH) so the caller leaves it untouched. This runs regardless of
      # the check's canonical-host/allowed-hosts config — an allowlisted
      # GitHub repo still gets its transport normalized to SSH, it just isn't
      # required to be on the forge.
      #
      # github.com keeps its OWNER/REPO path; code.linenisgreat.com is
      # flat/owner-less (no OWNER segment) — but both reduce to "take whatever
      # path segment follows the host, strip a trailing slash and .git", so one
      # case body covers both hosts once the host itself is picked out.
      to_ssh() {
        case "$1" in
          https://github.com/* | http://github.com/* | git://github.com/*) host=github.com ;;
          https://code.linenisgreat.com/* | http://code.linenisgreat.com/* | git://code.linenisgreat.com/*)
            host=code.linenisgreat.com
            ;;
          *) return 1 ;;
        esac
        path=''${1#*://"$host"/} # [OWNER/]REPO[.git][/]
        path=''${path%/} # strip a trailing slash
        path=''${path%.git} # strip a trailing .git
        printf 'git@%s:%s.git' "$host" "$path"
      }

      rewrote=0
      while IFS= read -r name; do
        [ -n "$name" ] || continue

        # Configured, not effective. Reading the insteadOf-rewritten URL here
        # would make repair "convert" a rewrite that only exists on this machine
        # and write the result back over config — churning .git/config for a
        # remote that was already correct.
        url=$(git config --get "remote.$name.url" 2>/dev/null || true)
        if [ -n "$url" ] && ssh=$(to_ssh "$url") && [ "$ssh" != "$url" ]; then
          git remote set-url "$name" "$ssh"
          echo "git-remotes(#8): rewrote remote '$name' $url -> $ssh"
          rewrote=1
        fi

        # A separately-configured push URL (remote.<name>.pushurl) is rewritten
        # too; without an explicit pushurl, `set-url` above already covers push.
        if git config --get-all "remote.$name.pushurl" >/dev/null 2>&1; then
          purl=$(git config --get "remote.$name.pushurl" 2>/dev/null || true)
          if [ -n "$purl" ] && pssh=$(to_ssh "$purl") && [ "$pssh" != "$purl" ]; then
            git remote set-url --push "$name" "$pssh"
            echo "git-remotes(#8): rewrote push URL of remote '$name' $purl -> $pssh"
            rewrote=1
          fi
        fi
      done < <(git remote)

      if [ "$rewrote" -eq 0 ]; then
        echo "git-remotes(#8): no github.com/code.linenisgreat.com non-SSH remotes to rewrite"
      fi
    '';
  };
in
{
  options.linters.git-remotes = {
    enable = lib.mkEnableOption "the git-remotes SSH-only + canonical-host whole-tree check (repair rewrites github.com/code.linenisgreat.com https/http/git remotes to SSH; needs a live .git; non-sandbox lane only)";

    canonical-host = lib.mkOption {
      type = lib.types.str;
      default = "code.linenisgreat.com";
      example = "github.com";
      description = ''
        The host the `origin` remote MUST target (checked via `git remote
        get-url origin`). Checked against `origin` only — other remotes
        (a codeberg upstream, a vendored fork, …) may legitimately point
        elsewhere and are exempt from this rule (they're still subject to the
        SSH-transport rule above). Independent check from the transport rule:
        a repo can fail for either or both reasons.
      '';
    };

    allowed-hosts = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "github.com" ];
      description = ''
        Extra hosts `origin` may target instead of `canonical-host`, for a
        repo deliberately still elsewhere (e.g. a repo still on GitHub pending
        its own forge migration). Declaring a host here only widens which host
        the check accepts as canonical for THIS repo — it does not exempt
        `origin` from the SSH-transport rule, and repair still normalizes a
        non-SSH remote on an allowed host to SSH.
      '';
    };
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
