# conformist-justfile(7) AGGREGATES AND LEAVES: a leaf recipe (one with a body)
# must be verb-noun — its name must NOT be a bare verb (`test-go`, not `test`),
# because a bare verb names an aggregate (eng-design_patterns-justfile(7)
# ANTI-PATTERNS, "redundant nouns"). Whole-tree check (passes-files=false): reads
# `just --dump --dump-format json`, takes no file arguments.
#
# Exempt: the verb-noun-exempt release recipes `tag` / `release` (single-segment
# by convention, like justfile-recipe-names) and private recipes.
#
# See conformist-justfile(7) AGGREGATES AND LEAVES and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-leaf-noun;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-leaf-noun";
    runtimeInputs = with pkgs; [
      coreutils
      jq
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-leaf-noun: justfile missing at tree root" >&2
        exit 1
      }

      # Leaf recipes (have a body), not private, whose name has no noun (no `-`),
      # excluding the verb-noun-exempt release recipes tag/release.
      filter='.recipes | to_entries[]
        | select((.value.body | length) > 0)
        | select(.value.private | not)
        | .key
        | select((test("-") | not) and . != "tag" and . != "release")'

      # Capture (not `< <(...)`) so a just/jq failure aborts loudly instead of
      # reading as "no findings" — a check must never pass vacuously on a parse error.
      if ! offenders=$(just --dump --dump-format json | jq -r "$filter"); then
        echo "justfile-leaf-noun: failed to read recipes via just/jq" >&2
        exit 2
      fi

      fail=0
      while read -r name; do
        [ -n "$name" ] || continue
        echo "justfile-leaf-noun: leaf recipe '$name' is a bare verb; a leaf must be verb-noun (e.g. 'test-go', not 'test') — a bare verb names an aggregate (conformist-justfile(7) AGGREGATES AND LEAVES)" >&2
        fail=1
      done <<< "$offenders"

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-leaf-noun: every leaf recipe is verb-noun"
    '';
  };
in
{
  options.linters.justfile-leaf-noun = {
    enable = lib.mkEnableOption "the leaf-recipes-must-have-a-noun whole-tree check (conformist-justfile(7), conformist#17)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-leaf-noun = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
