# conformist-justfile(7) TASK HIERARCHY: aggregate recipes (no body, only
# dependencies) compose leaf recipes (those with a body). The eng spec's "every
# leaf belongs to exactly one aggregate" is a SHOULD with legitimate orphans, so
# conformist-justfile(7) pins the enforceable split:
#   - PIPELINE-VERB leaves (verb build/test/validate/verify/lint/codemod) MUST
#     belong to EXACTLY ONE aggregate — un-aggregated => unreachable from default;
#     in two => duplicated work.
#   - OTHER leaves (run/list/install/deploy/load/migrate/provision/restart/
#     bump/update/clean/debug/explore, plus tag/release) MAY be orphans but
#     MUST NOT belong to more than one.
#   - Private recipes are exempt.
# Whole-tree check (passes-files=false): reads `just --dump --dump-format json`,
# takes no file arguments. `mod`-imported child justfiles are included
# (conformist#85): each module scope's recipes are checked with the same bounds,
# ownership is matched on bare recipe names across scopes (the dump drops the
# `mod::` qualifier from dependencies, so a root aggregate owning `mod::leaf`
# is still credited; a bare name duplicated across scopes is skipped as
# ambiguous), and the verb comes from the recipe's own unqualified name.
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
      #
      # `mod`-imported child justfiles nest under `.modules.<name>` in the dump
      # (recipe keys unqualified) — the old filter read only the root `.recipes`
      # and silently skipped every module recipe (conformist#85). scopes()
      # recurses into modules, carrying the qualified prefix for reporting.
      #
      # Ownership is matched on BARE recipe names across all scopes: the dump
      # serializes a module-qualified dependency (`agg: mod::leaf`) as just
      # "leaf", dropping the qualifier, so exact-path matching is impossible.
      # A leaf whose bare name appears in more than one scope is skipped as
      # ambiguous (its owners cannot be attributed from the lossy dump); such
      # duplicates are rare and the alternative is false findings either way.
      # The verb is taken from the recipe's own unqualified name.
      # $aggs/$leaf/$owners/$pipeline/$all/$p/$bare/$dupes are jq bindings, not
      # shell vars — they MUST stay literal in the single-quoted program;
      # SC2016 misreads that.
      # shellcheck disable=SC2016
      filter='def scopes(p):
          {p: p, s: .},
          ((.modules // {}) | to_entries[] | .key as $k | .value | scopes(p + $k + "::"));
        (["build","test","validate","verify","lint","codemod"]) as $pipeline
        | [scopes("")] as $all
        | ([ $all[] | .p as $p | .s.recipes | to_entries[]
            | select((.value.body | length) == 0 and (.value.dependencies | length) > 0)
            | { agg: ($p + .key), deps: [ .value.dependencies[].recipe | split("::") | last ] } ]) as $aggs
        | ([ $all[] | .s.recipes | to_entries[]
            | select((.value.body | length) > 0) | select(.value.private | not) | .key ]
           | group_by(.) | map(select(length > 1) | .[0])) as $dupes
        | $all[] | .p as $p | .s.recipes | to_entries[]
        | select((.value.body | length) > 0)
        | select(.value.private | not)
        | .key as $bare
        | select(($dupes | index($bare)) == null)
        | ($p + $bare) as $leaf
        | ($bare | split("-")[0]) as $verb
        | ([ $aggs[] | select(.deps | index($bare)) | .agg ]) as $owners
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
