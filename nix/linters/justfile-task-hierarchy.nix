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
# Whole-tree check (passes-files=false): reads the native recipe model via
# `just --dump --dump-format model`, takes no file arguments.
#
# `mod`-imported child justfiles are included: the model's `recipes` is one FLAT
# list spanning the root and every module (conformist#89), so both the leaves
# being checked and the aggregates that own them are found in a single pass.
#
# OWNERSHIP IS NOW EXACT (conformist#85's remaining caveat, closed). The raw
# `--dump-format json` serialized a dependency as the depended-on recipe's BARE
# name, dropping the `mod::` qualifier, so ownership had to be matched on bare
# names across scopes and any leaf whose bare name appeared in more than one
# scope was SKIPPED as ambiguous — a silent hole. The model's
# `dependencies[].namepath` is resolved, so matching is by fully-qualified
# identity and the ambiguity-skip is deleted: a duplicated bare name across two
# modules is now checked correctly rather than ignored.
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
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-task-hierarchy";
    justPackage = cfg.justPackage;
    okMessage = "leaf/aggregate membership is well-formed";
    # For each leaf recipe (body, not private), count the aggregates that list it
    # directly by NAMEPATH, then apply the per-verb bound: a pipeline verb needs
    # exactly one owner, any other verb at most one.
    filter = ''
      ["build","test","validate","verify","lint","codemod"] as $pipeline
      | recipes as $all
      | [ $all[]
          | select(isAggregate)
          | { agg: .namepath, deps: [ .dependencies[].namepath ] } ] as $aggs
      | $all[]
      | select(isLeaf)
      | select(.private | not)
      | . as $r
      | ([ $aggs[] | select(.deps | index($r.namepath)) | .agg ]) as $owners
      | ($owners | length) as $n
      | (($pipeline | index($r | verb)) != null) as $isPipeline
      | if $isPipeline and $n == 0 then
          "leaf \($r.namepath) (pipeline verb \($r | verb)) belongs to no aggregate; a pipeline-verb leaf must be in exactly one aggregate (conformist-justfile(7) TASK HIERARCHY)"
        elif $n > 1 then
          "leaf \($r.namepath) is listed in \($n) aggregates (\($owners | join(", "))); a leaf belongs to at most one aggregate (conformist-justfile(7) TASK HIERARCHY)"
        else empty end
    '';
  };
in
{
  options.linters.justfile-task-hierarchy = {
    enable = lib.mkEnableOption "the no-leaf-in-multiple-aggregates whole-tree check (eng-design_patterns-justfile(7), conformist#17)";
    justPackage = model.mkJustPackageOption "justfile-task-hierarchy";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-task-hierarchy = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
