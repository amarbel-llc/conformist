conformist-justfile(7)

# NAME

conformist-justfile - justfile conventions conformist enforces

# DESCRIPTION

conformist self-enforces the justfile conventions of
*eng-design_patterns-justfile*(7) through its whole-tree *justfile-\** linters.
This page is the normative, *enforceable* home for those linters: it states each
rule in the precise form a static check applies, citing
*eng-design_patterns-justfile*(7) as the prose origin. Where that page states a
convention as a SHOULD with legitimate exceptions, this page pins the exact
exemptions so the rule becomes a hard, checkable MUST.

Each rule names the linter that enforces it. The linters read recipe metadata
from _just --dump --dump-format json_.

# VERB LIST

Every recipe name is *verb-noun* (*eng-design_patterns-justfile*(7) NAMING
CONVENTION). The verb is the first _-_-delimited segment and MUST be one of the
canonical verbs:

	build test validate verify lint run list codemod
	install deploy load bump update clean debug explore

The version-release recipes *tag* and *release* and the entrypoint *default* are
exempt from verb-noun. Enforced by *justfile-recipe-names*. This list is
conformist's source of truth for that linter; it mirrors
*eng-design_patterns-justfile*(7) VERB CATEGORIES, which is the prose origin —
keep the two in sync.

# AGGREGATES AND LEAVES

A recipe is an *aggregate* when it has no body and only lists dependencies, and a
*leaf* when it has a body (*eng-design_patterns-justfile*(7) TASK HIERARCHY).

- *default* MUST be the first recipe and MUST list only aggregates. Enforced by
  *justfile-default*.
- A *leaf* MUST have a noun: its name MUST NOT be a bare verb (_test-go_, not
  _test_). A bare verb names an aggregate. Enforced by *justfile-leaf-noun*
  (*eng-design_patterns-justfile*(7) ANTI-PATTERNS, "redundant nouns").
- An *aggregate* MUST NOT carry a doc comment — its dependency list is
  self-documenting. Enforced by *justfile-aggregate-comments*
  (*eng-design_patterns-justfile*(7) ANTI-PATTERNS, "comments on aggregates").

# TASK HIERARCHY

*eng-design_patterns-justfile*(7) states "every leaf belongs to exactly one
aggregate." That is a SHOULD: standalone operational and maintenance recipes
legitimately belong to no aggregate. conformist pins the enforceable form, split
on the leaf's verb:

- *Pipeline-verb leaves* — verb _build_, _test_, _validate_, _verify_, _lint_, or
  _codemod_ — MUST belong to *exactly one* aggregate. These are the recipes the
  build-and-verify pipeline composes: an un-aggregated one is unreachable from
  _default_, and one in two aggregates is duplicated work.
- *Other leaves* — verb _run_, _list_, _install_, _deploy_, _load_, _bump_,
  _update_, _clean_, _debug_, _explore_, plus _tag_ and _release_ — MAY be
  orphans (standalone), but MUST NOT belong to more than one aggregate.
- *Private* recipes (underscore-prefixed or _[private]_) are exempt entirely.

Enforced by *justfile-task-hierarchy*.

# MODULES

A _mod_-imported child justfile's recipes are addressed module-qualified
(_explore::debug-widget_, nested _a::b::c_). The naming rules apply to the
recipe's *own* name: linters strip every leading module qualifier before verb
extraction (conformist#85). *justfile-recipe-names* checks
every module recipe; *justfile-task-hierarchy* checks each module scope with
the same per-verb bounds, crediting a root aggregate that lists _mod::leaf_ —
ownership is matched on bare recipe names, because _just --dump_ drops the
module qualifier from dependency entries, and a bare name duplicated across
scopes is skipped as ambiguous.

# RECIPE DESCRIPTIONS

Every *leaf* recipe MUST carry a doc comment: the single line immediately above
it (or an explicit _[doc("...")]_), which _just --list_ shows as the
description. Aggregates are exempt (see AGGREGATES AND LEAVES). debug/explore
leaves are checked with a throwaway-specific message by
*justfile-debug-recipes*; all other leaves by *justfile-recipe-descriptions*
(*eng-design_patterns-justfile*(7) RECIPE DESCRIPTIONS).

# LIFECYCLE GROUPS

A recipe's lifecycle group is *derived from its verb*
(*eng-design_patterns-justfile*(7) LIFECYCLE GROUPS gives the verb-to-stage
mapping). An explicit _[group("...")]_ attribute is therefore only needed when
the group differs from the verb-implied stage — a cross-axis recipe such as a
_verify-\*_ that actually runs pre-build — or to mark _debug_ / _explore_
throwaways. conformist does not require an explicit group on every recipe, so
there is no linter for it; this section records the convention so the absence of
enforcement is deliberate, not an oversight.

# SEE ALSO

*conformist*(7), *conformist-nix*(7), *conformist.toml*(5),
*eng-design_patterns-justfile*(7)
