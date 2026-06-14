# conformist-justfile(7) TASK HIERARCHY: aggregate recipes (no body, only
# dependencies) compose leaf recipes (those with a body). The eng spec's "every
# leaf belongs to exactly one aggregate" is a SHOULD with legitimate orphans, so
# conformist-justfile(7) pins the enforceable split:
#   - PIPELINE-VERB leaves (verb build/test/validate/verify/lint/codemod) MUST
#     belong to EXACTLY ONE aggregate — un-aggregated => unreachable from default;
#     in two => duplicated work.
#   - OTHER leaves (run/list/install/deploy/load/bump/update/clean/debug/explore,
#     plus tag/release) MAY be orphans but MUST NOT belong to more than one.
#   - Private recipes are exempt.
# Whole-tree check (passes-files=false): reads `just --dump --dump-format json`,
# takes no file arguments.
#
# See conformist-justfile(7) TASK HIERARCHY and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-task-hierarchy;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-task-hierarchy";
    runtimeInputs = with pkgs; [
      coreutils
      jq
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-task-hierarchy: justfile missing at tree root" >&2
        exit 1
      }

      # For each leaf recipe (body, not private), count the aggregate recipes (no
      # body, has dependencies) that list it directly, then apply the per-verb
      # bound. The leaf's verb is its first `-`-segment; pipeline verbs require
      # exactly one owner, others at most one.
      # $root/$aggs/$leaf/$owners/$pipeline are jq bindings, not shell vars — they
      # MUST stay literal in the single-quoted program; SC2016 misreads that.
      # shellcheck disable=SC2016
      filter='. as $root
        | (["build","test","validate","verify","lint","codemod"]) as $pipeline
        | ([ $root.recipes | to_entries[]
            | select((.value.body | length) == 0 and (.value.dependencies | length) > 0) ]) as $aggs
        | $root.recipes | to_entries[]
        | select((.value.body | length) > 0)
        | select(.value.private | not)
        | .key as $leaf
        | ($leaf | split("-")[0]) as $verb
        | ([ $aggs[] | select(any(.value.dependencies[]; .recipe == $leaf)) | .key ]) as $owners
        | ($owners | length) as $n
        | (($pipeline | index($verb)) != null) as $isPipeline
        | if $isPipeline and $n == 0 then
            "leaf \($leaf) (pipeline verb \($verb)) belongs to no aggregate; a pipeline-verb leaf must be in exactly one aggregate"
          elif $n > 1 then
            "leaf \($leaf) is listed in \($n) aggregates (\($owners | join(", "))); a leaf belongs to at most one aggregate"
          else empty end'

      # Capture (not `< <(...)`) so a just/jq failure aborts loudly instead of
      # yielding an empty stream that reads as "no findings" — a check must never
      # pass vacuously on its own parse error.
      if ! offenders=$(just --dump --dump-format json | jq -r "$filter"); then
        echo "justfile-task-hierarchy: failed to read recipes via just/jq" >&2
        exit 2
      fi

      fail=0
      while read -r line; do
        [ -n "$line" ] || continue
        echo "justfile-task-hierarchy: $line (conformist-justfile(7) TASK HIERARCHY)" >&2
        fail=1
      done <<< "$offenders"

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-task-hierarchy: leaf/aggregate membership is well-formed"
    '';
  };
in
{
  options.linters.justfile-task-hierarchy = {
    enable = lib.mkEnableOption "the no-leaf-in-multiple-aggregates whole-tree check (eng-design_patterns-justfile(7), conformist#17)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-task-hierarchy = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
