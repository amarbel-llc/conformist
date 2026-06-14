# conformist-justfile(7) AGGREGATES AND LEAVES: an aggregate recipe (no body,
# only dependencies) must NOT carry a doc comment — its dependency list is
# self-documenting (eng-design_patterns-justfile(7) ANTI-PATTERNS, "comments on
# aggregates"). The inverse of justfile-recipe-descriptions, which requires LEAF
# recipes to be documented. Whole-tree check (passes-files=false): reads
# `just --dump --dump-format json`, takes no file arguments.
#
# See conformist-justfile(7) AGGREGATES AND LEAVES and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-aggregate-comments;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-aggregate-comments";
    runtimeInputs = with pkgs; [
      coreutils
      jq
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-aggregate-comments: justfile missing at tree root" >&2
        exit 1
      }

      # Aggregate recipes (no body, has dependencies) that carry a doc comment.
      # `just` derives `doc` from the comment immediately above the recipe.
      filter='.recipes | to_entries[]
        | select((.value.body | length) == 0 and (.value.dependencies | length) > 0)
        | select((.value.doc // "") != "")
        | .key'

      # Capture (not `< <(...)`) so a just/jq failure aborts loudly instead of
      # reading as "no findings" — a check must never pass vacuously on a parse error.
      if ! offenders=$(just --dump --dump-format json | jq -r "$filter"); then
        echo "justfile-aggregate-comments: failed to read recipes via just/jq" >&2
        exit 2
      fi

      fail=0
      while read -r name; do
        [ -n "$name" ] || continue
        echo "justfile-aggregate-comments: aggregate recipe '$name' has a doc comment; aggregates are self-documenting via their dependency list — drop the comment (conformist-justfile(7) AGGREGATES AND LEAVES)" >&2
        fail=1
      done <<< "$offenders"

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-aggregate-comments: no aggregate recipe carries a doc comment"
    '';
  };
in
{
  options.linters.justfile-aggregate-comments = {
    enable = lib.mkEnableOption "the aggregates-must-not-be-commented whole-tree check (conformist-justfile(7), conformist#17)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-aggregate-comments = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
