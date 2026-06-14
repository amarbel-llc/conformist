# conformist-justfile(7) RECIPE DESCRIPTIONS: every leaf recipe carries a
# doc comment — the single comment line immediately above it, which `just --list`
# shows as the recipe's description. Whole-tree check (passes-files=false): reads
# recipe metadata from `just --dump --dump-format json`, takes no file arguments.
# Prose origin: eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS.
#
# Scope: LEAF recipes only (those with a body). Aggregate recipes (no body, only
# dependencies) are self-documenting via their dependency list and are exempt.
# debug/explore recipes are excluded here because justfile-debug-recipes (#23)
# already requires their doc comment with a throwaway-specific message, so this
# rule would otherwise double-report them. Private recipes (underscore-prefixed or
# [private]) don't appear in `just --list` and are exempt too.
#
# See eng-design_patterns-justfile(7) and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-recipe-descriptions;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-recipe-descriptions";
    runtimeInputs = with pkgs; [
      coreutils
      jq
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-recipe-descriptions: justfile missing at tree root" >&2
        exit 1
      }

      # Leaf recipes (have a body), excluding private and debug/explore recipes,
      # whose doc comment is empty. A group attribute serializes as
      # {"group":"<name>"}; `just` derives `doc` from the comment above the recipe.
      filter='.recipes | to_entries[]
        | select((.value.body | length) > 0)
        | select(.value.private | not)
        | select(([.value.attributes[]? | .group?] | any(. == "debug" or . == "explore")) | not)
        | select((.value.doc // "") == "")
        | .key'

      # Capture (not `< <(...)`) so a just/jq failure aborts loudly instead of
      # reading as "no findings" — a check must never pass vacuously on a parse error.
      if ! undocumented=$(just --dump --dump-format json | jq -r "$filter"); then
        echo "justfile-recipe-descriptions: failed to read recipes via just/jq" >&2
        exit 2
      fi

      fail=0
      while read -r name; do
        [ -n "$name" ] || continue
        echo "justfile-recipe-descriptions: leaf recipe '$name' has no doc comment; add a one-line summary immediately above it (eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS)" >&2
        fail=1
      done <<< "$undocumented"

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-recipe-descriptions: all leaf recipes carry a doc comment"
    '';
  };
in
{
  options.linters.justfile-recipe-descriptions = {
    enable = lib.mkEnableOption "the leaf-recipes-must-be-documented whole-tree check (eng-design_patterns-justfile(7), conformist#17)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-recipe-descriptions = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
