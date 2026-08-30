# Shared model-consumption helper for the seven `justfile-*` whole-tree linters.
#
# Every one of them used to shell out to `just --dump --dump-format json` and
# reconstruct recipe structure with jq — re-deriving what just's parser already
# computed and threw away. Two structural defects followed directly from that
# reconstruction, and both are fixed here by DELETING the reconstruction rather
# than patching it:
#
#   - conformist#85: the verb was extracted by splitting the recipe's key on
#     `-`, but a `mod`-imported recipe surfaces fully qualified
#     (`explore::debug-foo`), so the "verb" came out `explore::debug` and never
#     matched an allowlist. The model carries the BARE `name` and the `module`
#     path as separate fields, so splitting `name` is correct with no stripping
#     step at all.
#   - conformist#89: five of the seven read only the root `.recipes` and never
#     recursed into `.modules`, silently skipping every module recipe. The
#     model's `recipes` is a single FLAT list spanning the root and all modules,
#     so there is no subtree left to forget to recurse into. Routing all seven
#     through THIS helper is the other half of that fix: the roster cannot drift
#     apart again, because there is only one place that reads the model.
#
# A third defect went with them: the raw dump serializes a dependency as the
# depended-on recipe's BARE name (`keyed::serialize`), dropping any `mod::`
# qualifier, so justfile-task-hierarchy had to match ownership on ambiguous bare
# names and SKIP any leaf whose name appeared in more than one scope. The model's
# `dependencies[].namepath` is resolved, so that ambiguity-skip is gone.
#
# POLICY BOUNDARY (just-us FDR 0003, "Policy boundary"). The model is DATA only:
# it deliberately emits no `is_aggregate`, `is_leaf`, or `verb`. conformist keeps
# the eng POLICY — the verb allowlist, the lifecycle groups, the aggregate/leaf
# taxonomy — and applies it over the model's raw signals (`has_body`,
# `dependencies`, bare `name`, `groups`). The taxonomy definitions below are that
# policy; keep them here and out of just-us.
#
# THE FORK REQUIREMENT. `--dump-format model` is a just-us fork format; a stock
# `just` rejects it outright. That is a LOUD failure (the check exits 2), not a
# vacuous pass — unlike `doc_prelude`, whose absence a stock `just` would report
# as zero findings, which is why just-us's own justfile-orphan-summary module
# makes its `justPackage` mandatory. Here the option carries a `pkgs.just`
# default plus the diagnostic below, so a repo that has not yet wired just-us
# fails its lint with an actionable message instead of failing to EVALUATE.
#
# Usage: each linter module imports this and calls it with its own jq filter.
# The filter runs after the shared prelude and emits ONE LINE PER FINDING; the
# helper prefixes each with the linter name and exits 1, or prints okMessage and
# exits 0. The jq program is written to the store and passed with `jq -f`, so a
# finding message may contain single quotes without any shell-quoting hazard
# (the reason the old filters kept their human text in shell and passed tagged
# `FIRST:`/`DEP:` discriminants back out).
{ pkgs, lib }:

let
  # jq definitions shared by every filter. This is the "shared prelude" that
  # conformist#89's fix shape calls for: one place that knows the model's shape,
  # pins its version, and states the eng taxonomy.
  prelude = ''
    # --- conformist justfile-* model prelude (just-us.recipe-model v1) -------

    # Pin the contract. The FDR's versioning rule is that additive fields do NOT
    # bump `version`, so a consumer pins the integer and tolerates growth; a bump
    # means a breaking change and MUST stop us rather than silently produce an
    # empty finding stream that reads as a clean tree.
    def model:
      if .schema != "just-us.recipe-model" then
        error("unexpected schema '\(.schema // "<absent>")'; expected 'just-us.recipe-model'")
      elif .version != 1 then
        error("unsupported recipe-model version '\(.version // "<absent>")'; this check pins version 1")
      else . end;

    # Every recipe across the root AND all modules, already flattened by just
    # (conformist#89). Sorted by namepath, so findings come out in a stable order.
    def recipes: model | .recipes;

    # Private recipes (underscore-prefixed or [private]) never appear in
    # `just --list`, so most rules exempt them. Applied to a LIST.
    def public: map(select(.private | not));

    # Index the flat list by namepath, for resolving a dependency to the recipe
    # it names. Applied to a LIST.
    def byNamepath: map({ key: .namepath, value: . }) | from_entries;

    # --- eng taxonomy (conformist POLICY, not model data) --------------------

    # A leaf does work: it has body lines. An aggregate composes: no body, only
    # dependencies. `has_body` is strictly `!body.is_empty()`, so a recipe with
    # BOTH a body and dependencies is a leaf, not an aggregate.
    def isLeaf: .has_body;
    def isAggregate: (.has_body | not) and ((.dependencies | length) > 0);

    # The verb is the first `-`-segment of the recipe's BARE name. `name` never
    # carries a `mod::` qualifier — that lives in `module`/`namepath` — so this
    # is correct for a module recipe with no stripping (conformist#85).
    def verb: .name | split("-") | .[0];

    # --- end prelude ---------------------------------------------------------
  '';
in
{
  # name       : linter name (the settings.linter.<name> key; prefixes findings)
  # justPackage: the `just` whose binary is invoked. MUST be a just-us build —
  #              see THE FORK REQUIREMENT above.
  # filter     : jq program run after the prelude, emitting one line per finding
  # okMessage  : printed on a clean tree
  mkModelCheck =
    {
      name,
      justPackage,
      filter,
      okMessage,
    }:
    let
      program = pkgs.writeText "conformist-${name}.jq" (prelude + "\n" + filter);
    in
    pkgs.writeShellApplication {
      name = "conformist-${name}";
      runtimeInputs = [
        pkgs.coreutils
        pkgs.jq
        justPackage
      ];
      text = ''
        [ -f justfile ] || {
          echo "${name}: justfile missing at tree root" >&2
          exit 1
        }

        # Capture (not `< <(...)`) so a just/jq failure aborts loudly instead of
        # yielding an empty stream that reads as "no findings" — a check must
        # never pass vacuously on its own parse error. The jq program lives in
        # the store and is passed with -f, so nothing here needs shell quoting.
        if ! findings=$(just --dump --dump-format model | jq -r -f ${program}); then
          echo "${name}: failed to read the recipe model via just/jq." >&2
          echo "${name}: this check requires the just-us fork (code.linenisgreat.com/just-us) - '--dump-format model' is a fork-only format that a stock 'just' rejects. Set linters.${name}.justPackage to just-us.packages.<system>.default (or fix the justfile error just reported above)." >&2
          exit 2
        fi

        fail=0
        while read -r line; do
          [ -n "$line" ] || continue
          echo "${name}: $line" >&2
          fail=1
        done <<< "$findings"

        if [ "$fail" -ne 0 ]; then
          exit 1
        fi
        echo "${name}: ${okMessage}"
      '';
    };

  # The shared `justPackage` option every justfile-* linter declares. Defaulting
  # to `pkgs.just` keeps `imports = [ conformist.lib.presets.eng ]` EVALUATING
  # for a repo that has not wired just-us yet — the failure then lands at lint
  # time with the actionable message above, rather than as an eval error that
  # blocks the repo entirely. Making it mandatory (no default, as
  # justfile-orphan-summary does) is a one-line change here once the fleet has
  # migrated.
  mkJustPackageOption =
    name:
    lib.mkOption {
      type = lib.types.package;
      default = pkgs.just;
      defaultText = lib.literalExpression "pkgs.just";
      description = ''
        The `just` package whose binary this check invokes. MUST be a just-us
        build (code.linenisgreat.com/just-us): the check reads
        `just --dump --dump-format model`, a fork-only dump format that a stock
        `just` rejects. Set it to `just-us.packages.<system>.default`, the same
        `justPkg` binding the eng template already uses for
        `linters.justfile-orphan-summary.justPackage`.

        The `pkgs.just` default exists so enabling this linter (e.g. via
        `conformist.lib.presets.eng`) still EVALUATES in a repo that has not
        wired just-us yet; the check then fails at lint time with a message
        naming this option (${name}), rather than passing vacuously — a stock
        `just` rejects the format outright instead of silently omitting a field.
      '';
    };
}
