# eng-design_patterns-justfile(7) TASK HIERARCHY: bare-verb aggregate recipes (no
# body, only dependencies) compose verb-noun leaf recipes (those with a body).
# The spec's "every leaf belongs to exactly one aggregate" is a SHOULD with
# legitimate orphans (standalone maintenance/operational recipes — release, tag,
# run-nix — belong to no aggregate by design), so the lower bound is not cleanly
# lintable. This check enforces the UPPER bound, which is unambiguous and
# low-false-positive: a leaf must not be listed in MORE than one aggregate
# (duplicate wiring — running one aggregate silently re-runs work another already
# covers). Whole-tree check (passes-files=false): reads `just --dump
# --dump-format json`, takes no file arguments.
#
# See eng-design_patterns-justfile(7) and amarbel-llc/conformist#17.
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

      # For each leaf recipe (has a body), count how many aggregate recipes (no
      # body, has dependencies) list it directly; report those listed in >1.
      # $root/$aggs/$leaf/$owners are jq bindings, not shell vars — they MUST stay
      # literal in the single-quoted program; SC2016 misreads that as a bug.
      # shellcheck disable=SC2016
      filter='. as $root
        | ([ $root.recipes | to_entries[]
            | select((.value.body | length) == 0 and (.value.dependencies | length) > 0) ]) as $aggs
        | $root.recipes | to_entries[]
        | select((.value.body | length) > 0)
        | .key as $leaf
        | ([ $aggs[] | select(any(.value.dependencies[]; .recipe == $leaf)) | .key ]) as $owners
        | select(($owners | length) > 1)
        | "\($leaf) is listed in \($owners | length) aggregates: \($owners | join(", "))"'

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
        echo "justfile-task-hierarchy: $line — a leaf belongs to at most one aggregate (eng-design_patterns-justfile(7) TASK HIERARCHY)" >&2
        fail=1
      done <<< "$offenders"

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-task-hierarchy: no leaf recipe belongs to more than one aggregate"
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
