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
      gitMinimal # core subcommands only; full git's closure drags in python3 (#62)
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

      # 3. Reachability (opt-in, conformist#98): rules 1 and 2 read only the
      # scheme and host, never whether the remote EXISTS, so a remote left
      # dangling by a forge rename (git@host:old-name.git) passes both. With
      # `probe`, ask git whether origin answers. Origin only, like the host rule:
      # another upstream may be down or private on purpose. Uses the EFFECTIVE
      # URL (insteadOf applied) because reachability is about what git will
      # really contact. `ls-remote` without --exit-code exits 0 for a reachable
      # remote with no refs yet, so a fresh empty repo is not flagged.
      #
      # Needs the network and the operator's credentials, so it is off by
      # default, and bounded so a hung connection cannot stall the lane.
      probe=${if cfg.probe then "1" else "0"}
      if [ "$probe" -eq 1 ] && git config --get remote.origin.url >/dev/null 2>&1; then
        if ! probe_out=$(GIT_TERMINAL_PROMPT=0 timeout ${toString cfg.probe-timeout} git ls-remote origin 2>&1 >/dev/null); then
          echo "git-remotes(#98): origin is unreachable (git ls-remote origin failed): $(git config --get remote.origin.url)" >&2
          printf '%s\n' "$probe_out" | sed 's/^/  /' >&2
          echo "  (a renamed or deleted repository passes the scheme and host rules but cannot be fetched or pushed)" >&2
          findings=1
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
      gitMinimal # core subcommands only; full git's closure drags in python3 (#62)
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

    probe = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Also check that `origin` EXISTS, by running `git ls-remote origin`
        (conformist#98). Without it a green result means scheme and host only: a
        remote left dangling by a forge rename (`git@host:old-name.git`) satisfies
        both rules and still fails every push. A reachable remote with no refs yet
        passes. Origin only, like the host rule.

        Off by default because it needs the network and the operator's
        credentials, so it can fail for reasons unrelated to the repository. Run
        it where that holds (the impure working-tree lane). A whole-tree check's
        clean result may be cached (conformist#16), so pass `--no-cache` when the
        probe must really run.
      '';
    };

    probe-timeout = lib.mkOption {
      type = lib.types.ints.positive;
      default = 30;
      description = "Seconds `probe` waits for `git ls-remote` before treating origin as unreachable.";
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
