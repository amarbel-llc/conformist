# conformist-nix(7) FIRST-PARTY FLAKE INPUTS (conformist#150): a flake input whose
# owner is first-party must be fetched from the forge, not GitHub. `nix flake
# update` makes one api.github.com call per floating github: input, and GitHub's
# rate limit halted the fleet's update cascade; first-party code lives on the
# forge. Whole-tree check (passes-files=false) reading only committed files
# (flake.nix + flake.lock), so it runs in the sandboxed checks.formatting.
#
# R1 (the rule): an input is a violation when its owner is in `owners` and it is
# written as github:O/R, git+https://github.com/O/R, a git+ssh/ssh URL on
# github.com, or a github.com/O/R/archive tarball.
# R2 (where): flake.nix is scanned for the declared URL (error); flake.lock is
# scanned for every node's `original`. A direct input in flake.lock is an error
# (it catches inputs flake.nix spells indirectly); a transitive node that is a
# first-party GitHub input is only a WARNING, reported against the direct input
# that brought it in (the fix is a `follows` or a bump in that input's repo).
#
# Exemption: `exempt-inputs` downgrades named direct inputs to warnings;
# `warn-only` downgrades the whole check (eng uses it until FDR 0044 stage 5).
# Room for R3 (any github: input outside an allowlist, naming a mirror URL): the
# per-input classification below is the single place such a rule would hook in;
# it is deliberately not built. See conformist-nix(7) FIRST-PARTY FLAKE INPUTS.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.flake-first-party-inputs;

  # The first-party owners, burned into the signed profile (conformist.profile,
  # [prelude.first-party-owners]) and read here at eval time: a pure read of a
  # committed file, so the sandboxed check stays offline. Matches the prelude's
  # one-line `def`; a profile edit that breaks the shape fails eval loudly.
  profileOwners =
    let
      def = builtins.split "def first_party_owners: ([^;]*);" (
        builtins.readFile ../../conformist.profile
      );
      quoted = builtins.split "\"([^\"]*)\"" (builtins.elemAt (builtins.elemAt def 1) 0);
    in
    if builtins.length def != 3 then
      throw "flake-first-party-inputs: conformist.profile has no one-line `def first_party_owners: [...]` prelude"
    else
      map (m: builtins.elemAt m 0) (builtins.filter builtins.isList quoted);

  check = pkgs.writeShellApplication {
    name = "conformist-flake-first-party-inputs";
    runtimeInputs = with pkgs; [
      coreutils
      gawk
      gnugrep
      gnused
      jq
    ];
    text = ''
      owners=${lib.escapeShellArg (lib.concatStringsSep "," (map lib.toLower cfg.owners))}
      forge=${lib.escapeShellArg cfg.forge-url}
      warn_only=${if cfg.warn-only then "1" else "0"}
      exempt=" ${lib.concatStringsSep " " cfg.exempt-inputs} "

      [ -f flake.nix ] || {
        echo "flake-first-party-inputs: no flake.nix at tree root; nothing to check"
        exit 0
      }

      errors=0

      # report <input> <where> <repo> <ref> <detail>: one violation of a DIRECT
      # input. Exempt or warn-only inputs are warnings; the rest fail the check.
      report() {
        local input=$1 where=$2 repo=$3 ref=$4 detail=$5 level=error
        [ "$warn_only" = 1 ] && level=warning
        case "$exempt" in *" $input "*) level=warning ;; esac
        echo "conformist-nix(7) FIRST-PARTY FLAKE INPUTS: $level: $where: input '$input' is first-party ($detail); use $forge/$repo/archive/''${ref:-<ref>}.tar.gz" >&2
        [ "$level" = error ] && errors=$((errors + 1))
        return 0
      }

      alt=$(printf '%s' "$owners" | tr ',' '|')

      # ---- flake.nix: the declared URL (R2, error) ----------------------------
      pattern="(github:|git[+]https://github[.]com/|git[+]ssh://git@github[.]com/|ssh://git@github[.]com/|git@github[.]com:|https://github[.]com/)($alt)/[A-Za-z0-9_.-]+"
      sed 's/#.*$//' flake.nix | awk -v pat="$pattern" '
        match($0, /^[ \t]*(inputs\.)?[A-Za-z_][A-Za-z0-9_-]*[ \t]*=[ \t]*\{/) {
          n = $0; sub(/^[ \t]*(inputs\.)?/, "", n); sub(/[ \t]*=.*$/, "", n)
          if (n != "inputs") last = n
        }
        tolower($0) ~ pat {
          name = last
          if (match($0, /[A-Za-z_][A-Za-z0-9_-]*\.url[ \t]*=/)) {
            name = substr($0, RSTART, RLENGTH); sub(/\.url.*$/, "", name)
          }
          print NR "\t" (name == "" ? "?" : name) "\t" tolower($0)
        }' |
        while IFS=$'\t' read -r line name text; do
          repo=$(printf '%s' "$text" | grep -oE "($alt)/[A-Za-z0-9_.-]+" | head -1 | sed -E 's#^[^/]*/##; s#\.git$##')
          ref=$(printf '%s' "$text" | sed -nE "s#.*($alt)/[A-Za-z0-9_.-]+[/?]([A-Za-z0-9_.-]+).*#\2#p" | sed -E 's#\.tar\.gz$##')
          [ "$ref" = archive ] && ref=
          printf '%s\t%s\t%s\t%s\n' "$line" "$name" "$repo" "$ref"
        done >flake-nix-hits.tmp || true

      while IFS=$'\t' read -r line name repo ref; do
        [ -n "$line" ] || continue
        report "$name" "flake.nix:$line" "$repo" "$ref" "GitHub URL for $repo"
      done <flake-nix-hits.tmp
      rm -f flake-nix-hits.tmp

      # ---- flake.lock: every node's original (R2) -----------------------------
      if [ -f flake.lock ]; then
        jq -r --arg owners "$owners" '
          . as $l
          | ($owners | split(",")) as $own
          | ($own | join("|")) as $alt
          | def bad($n):
              ($l.nodes[$n].original // {}) as $o
              | (($o.type == "github") and ((($o.owner // "") | ascii_downcase) as $x | $own | index($x) != null))
                or ((($o.type == "git") or ($o.type == "tarball"))
                    and (($o.url // "") | test("github\\.com[/:](" + $alt + ")/"; "i")));
            def repo($n):
              ($l.nodes[$n].original // {}) as $o
              | $o.repo // ($o.url | capture("github\\.com[/:][^/]+/(?<r>[^/]+)").r | sub("\\.git$"; "") | sub("\\.tar\\.gz$"; ""));
            def reach($seen):
              ([ $seen[] | ($l.nodes[.].inputs // {}) | .[] | select(type == "string") ] + $seen | unique) as $n
              | if ($n | length) == ($seen | length) then $seen else reach($n) end;
          ($l.nodes.root.inputs // {}) | to_entries[] | select(.value | type == "string") as $d
          | ($d.value) as $dn
          | (if bad($dn) then "direct\t\($d.key)\t\(repo($dn))\t\($l.nodes[$dn].original.ref // "")" else empty end),
            ( reach([$dn]) - [$dn] | .[] | select(. != "root") | select(bad(.))
              | "trans\t\($d.key)\t\(repo(.))\t\($l.nodes[.].original.ref // "")" )
        ' flake.lock >flake-lock-hits.tmp

        while IFS=$'\t' read -r kind input repo ref; do
          [ -n "$kind" ] || continue
          if [ "$kind" = direct ]; then
            report "$input" "flake.lock" "$repo" "$ref" "locked original is GitHub for $repo"
          else
            echo "conformist-nix(7) FIRST-PARTY FLAKE INPUTS: warning: flake.lock: transitive first-party GitHub input $repo reached via direct input '$input'; follow or bump it there so it uses $forge/$repo/archive/''${ref:-<ref>}.tar.gz" >&2
          fi
        done <flake-lock-hits.tmp
        rm -f flake-lock-hits.tmp
      fi

      if [ "$errors" -gt 0 ]; then
        exit 1
      fi
      echo "flake-first-party-inputs: no first-party flake input is fetched from GitHub"
    '';
  };
in
{
  options.linters.flake-first-party-inputs = {
    enable = lib.mkEnableOption "the first-party-flake-inputs-on-the-forge whole-tree check (conformist#150)";

    owners = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = profileOwners;
      defaultText = lib.literalMD "the `first_party_owners` prelude of `conformist.profile`";
      description = "GitHub owners whose repos are first-party; their inputs must come from the forge. Defaults to the list signed into the conformist profile; set it to override per repo.";
    };

    forge-url = lib.mkOption {
      type = lib.types.str;
      default = "https://code.linenisgreat.com";
      description = "Forge base URL used in the canonical-URL part of the message.";
    };

    exempt-inputs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Direct input names reported as warnings instead of errors.";
    };

    warn-only = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Report every direct-input violation as a warning (exit 0), e.g. while a fleet migration is pending.";
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.flake-first-party-inputs = {
      command = lib.getExe check;
      includes = [ "flake.nix" ];
      passes-files = false;
    };
  };
}
