---
status: exploring
date: 2026-06-29
---

# Pipeline Phases, Scope Composition, and Invocation Modes

## Abstract

This document specifies conformist's processing pipeline as an ordered set of
phases — **repair**, **format**, and **stage/check** — and defines, normatively,
how each phase's *scope* (the set of paths it acts on) is derived and composes
with the phases around it. It pins a single cross-mode invariant — **nothing is
staged or committed that the format phase was not given the chance to normalize,
and the format phase covers every repair output a formatter matches** — and
presents the phase behavior of every invocation mode (`nix fmt`, `check`,
`--commit`, `--commit --amend`, `--staged`) as one matrix. It exists because the
repair/format/stage interaction has been specified one mode and one edge case at
a time, producing a long tail of point-fixes (conformist#55/#56/#57/#67/#70 and
the open #44/#45/#66) that share a single missing model.

## Introduction

conformist began as a formatter multiplexer (a clean copy of treefmt) and grew a
**linter-repair** phase (RFC-0001 §4) and a **stage-mutation** phase (RFC-0002)
on top of the original **format** phase. It now exposes several invocation modes
— the bare command / `nix fmt`, the `check` subcommand, `--commit`,
`--commit --amend`, and `--staged` (with distinct fully-staged and
partially-staged sub-lanes) — and each mode recombines the three phases over a
mode-specific scope.

RFC-0002 modeled one axis of this machine well: the **stage-mutation capability**
axis, turning what would have been three ad-hoc restage patches into a coherent
tier-2/3/4 opt-in model. But two axes were left unmodeled, and that is where the
recurring thrash lives:

1. **Phase ordering and scope composition** — how the format phase's scope
   incorporates the repair phase's outputs. conformist#70 (a whole-tree repair
   rewriting a formatter-matched file outside the staged set, restaged
   unformatted) was a violation of this composition with no model to prevent it;
   it was fixed as a one-off (`formatScope`) for one mode.
2. **The mode × phase matrix** — which phases run, over what scope, in each
   invocation mode. The open conformist#44/#45 (linter "watch" set vs the
   formatter "don't-rewrite" global excludes) and conformist#66 (declarative
   per-hook membership) both live here.

This specification defines axes 1 and 2. It does **not** redefine RFC-0002's
stage-mutation tiers (the stage phase's *capability* sub-model) or RFC-0001's
check/repair modes and `passes-files` semantics; it builds on both. It is
**exploratory**: §2–§4 (the phase model, the scope-composition invariant, and
the mode matrix) are the immediately actionable core and are stated normatively;
§5 (universal repair-output observation) and §6 (the partial-stage lane) present
the harder design forks as options with a recommendation, with ratification
deferred pending review.

The model in this document was validated against the live `amarbel-llc/eng`
global `conformist.toml` (the canonical whole-tree-repair consumer; see §7). That
config's sole whole-tree repair linter, `[linter.doppelgang-flake]`, matches the
model's INV-2 subject exactly: a `passes-files = false` repair (`doppelgang lint
--fix`) that rewrites `flake.nix` (which `[formatter.nixfmt]` matches) and
re-locks `flake.lock` (which no formatter matches), opting into
`restage-repair-outputs`. It is the real shape conformist#70 was filed against.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### 1. Terminology

- **Phase** — one of the ordered processing stages a run performs: **repair**
  (run linter `repair-command`s), **format** (run formatter `command`s), **stage**
  (mutate the git index / create a commit), and **check** (read-only evaluation).
  A given invocation mode runs a subset of these phases.
- **Scope** — the set of toplevel-relative paths a phase acts on. Each phase
  derives its own scope; the central concern of this document is how those scopes
  compose.
- **Input paths** — the positional path arguments to the run (`conformist
  <paths>`), or empty for a whole-tree run.
- **Whole-tree scope** — the scope produced by an empty input-path set: the
  walker traverses the entire tree root (per the walk type).
- **Repair output** — a path a linter's `repair-command` modified, created, or
  deleted during the repair phase (RFC-0002 §1).
- **Repair-output set** — the union of all repair outputs across every linter in
  a run, discoverable by a tree-state delta taken around the whole repair phase.
  Distinct from RFC-0002's *per-linter* status delta, which attributes outputs to
  one linter for staging.
- **Invocation mode** — the run shape selected by the command and flags: bare /
  `nix fmt`, `check`, `--commit`, `--commit --amend`, `--staged`.
- **Stage-scope / format-scope / repair-scope** — the scope of the named phase
  in a given run.

### 2. The phase model

A run MUST execute its phases in this order:

1. **repair** (when not in `check` mode and not in stdin mode),
2. **format** (or, in `check` mode, **check**),
3. **stage** (only in `--commit`, `--commit --amend`, and `--staged`).

The repair phase MUST run before the format phase, so that formatters normalize
repair output (RFC-0001 §4). The stage phase MUST run after the format phase, so
that the content entering the index is the formatted content (§3, INV-1). `check`
mode MUST run neither the repair nor the stage phase: it is read-only (RFC-0001
§3).

The repair phase is a separate, cache-less walk from the format phase
(conformist#16's format cache governs only the format phase). This is an
implementation detail, but it has a normative consequence: because the two phases
are separate walks, the format phase's scope is computed *after* the repair phase
completes and therefore CAN incorporate the repair-output set (§3).

### 3. Scope composition (the core invariant)

This section defines the relationship the three scopes MUST satisfy. Two
invariants hold in every mode:

**INV-1 (format-before-stage).** A run MUST NOT stage or commit file content that
the format phase was not given the opportunity to normalize. Equivalently:

> stage-scope ⊆ format-scope

**INV-2 (repair-outputs-are-formatted).** The format-scope MUST include every
repair output that at least one configured formatter matches. Equivalently:

> format-scope ⊇ ( input-paths ∪ { p ∈ repair-output-set : some formatter matches p } )

A path in the repair-output set that no formatter matches (e.g. a regenerated
`flake.lock`, which conformist does not format) is NOT required to be in
format-scope by INV-2, because formatting it is a no-op.

Together, INV-1 and INV-2 give the property the point-fixes were each
re-deriving: **any path a repair writes that a formatter matches, and that the
run will stage, is formatted before it is staged.** conformist#70 was exactly an
INV-1 violation for the `--staged` mode (an opt-in repair output entered
format-scope only after #70's `formatScope`); the open `--commit`-with-explicit-
paths asymmetry (a repair output committed but not formatted) is the same
violation in another mode.

#### 3.1 Worked example

A whole-tree codegen-repair linter rewrites `flake.nix` (unformatted, by
byte-splice) and re-locks `flake.lock`; a `nixfmt` formatter matches
`flake.nix`. The author staged only `justfile`, then ran `conformist --staged`:

- repair-scope: whole tree (a `passes-files = false` linter triggers on its
  includes regardless of input paths). repair-output set: `{flake.nix,
  flake.lock}`.
- format-scope MUST (INV-2) be `{justfile} ∪ {flake.nix}` — `flake.lock` is
  excluded because no formatter matches it. So `flake.nix` is nixfmt-normalized
  in the working tree.
- stage-scope MUST (INV-1) be ⊆ format-scope. The `flake.nix` content staged is
  therefore the formatted content.

#### 3.2 Non-requirements

INV-1 and INV-2 constrain *content normalization*, not *which paths are staged*.
Whether a repair output is staged at all remains governed by RFC-0002's
stage-mutation tiers (a non-opt-in repair output is still left unstaged). INV-2
does not require formatting a repair output that will not be staged in modes
where staging is selective; an implementation MAY format only the
formatter-matched repair outputs that are within the stage-scope, as an
optimization, provided INV-1 still holds. The simplest conformant implementation
formats the full formatter-matched repair-output set.

### 4. The mode × phase matrix (normative)

The following table specifies, for each invocation mode, which phases run and
how each phase's scope is derived. `P` = input paths. `R` = the repair-output
set. `F(X)` = the subset of `X` matched by some formatter. `S` = the staged set
(toplevel-relative paths with an index change at run start).

| Mode | repair scope | format scope | stage scope |
|------|-------------|--------------|-------------|
| bare / `nix fmt` | `P` (∅ ⇒ whole tree); whole-tree linters trigger on includes | `P ∪ F(R)` | — (no stage phase) |
| `check` | — (no repair) | read-only check over `P`; whole-tree checks over their full match set | — |
| `--commit` | `P` (∅ ⇒ whole tree) | `P ∪ F(R)` | the run's net working-tree change, ⊆ format-scope (INV-1) |
| `--commit --amend` | as `--commit` | as `--commit` | as `--commit`, folded into HEAD |
| `--staged` (fully staged) | whole tree for whole-tree linters; `S`-derived for per-file | `S ∪ F(R)` | `S`-dirty ∪ (tier-permitted repair outputs, RFC-0002 §2) |
| `--staged` (partially staged) | see §6 (open) | see §6 (open) | object-store restage of changed staged blobs |

Normative reading of the table:

- In every mode that has a format phase, format-scope MUST equal `P ∪ F(R)` (or
  `S ∪ F(R)` for `--staged`), satisfying INV-2.
- In every mode that has a stage phase, stage-scope MUST be ⊆ format-scope,
  satisfying INV-1.
- `check` mode MUST run no repair and no stage phase.
- The `--staged` partially-staged sub-lane is the one cell this document does
  NOT yet make conformant to INV-1/INV-2; see §6.

A mode MUST NOT introduce a stage-scope path outside format-scope. An
implementation adding a new mode (e.g. conformist#66's per-hook membership) MUST
populate a row of this matrix and satisfy INV-1/INV-2 for it.

### 5. Universal repair-output observation (open)

INV-2 requires the format phase to know the repair-output set `R`. Today
conformist observes repair outputs ONLY for linters that opt into RFC-0002 tier 2
(`restage-repair-outputs`), via the per-linter status delta the staging phase
needs for attribution. The format phase reuses that same opt-in-scoped set
(conformist#70's `formatScope`), which means INV-2 currently holds **only for
opt-in linters** — a format-correctness property gated on a staging flag.

The insight this document draws out: there are **two distinct reasons** to observe
repair outputs, with different shapes:

- **Formatting** (INV-2) needs the *union* `R`, and does not need attribution.
  One tree-state delta around the whole repair phase suffices, and is cheap.
- **Staging** (RFC-0002 §2.5) needs *per-linter* attribution, and only for the
  tier-2/3/4 opt-in linters.

Conflating them — observing only the opt-in subset, for both purposes — is what
couples INV-2 to the staging opt-in.

**Options:**

- **Option A — universal observation (recommended).** The repair phase MUST
  report `R` via a single tree-state delta taken around the whole phase,
  independent of any per-linter opt-in. The format phase derives format-scope as
  `P ∪ F(R)`. RFC-0002's per-linter deltas remain, unchanged, for staging
  attribution. Cost: one extra `git status` (or filesystem snapshot) per run.
  Makes INV-2 universal — a non-opt-in repair's formatter-matched output is
  normalized too, closing the `--commit`-with-explicit-paths asymmetry (§3) and
  decoupling format-correctness from the staging flag.

- **Option B — status quo, documented boundary.** Keep observing only the opt-in
  subset. INV-2 holds only for opt-in linters; the matrix's `F(R)` term is read
  as `F(R_optin)`. Document that a non-opt-in whole-tree repair's
  formatter-matched output is committed/restaged unformatted in scoped modes.
  Lower cost, but leaves a latent INV-1 violation for `--commit` with explicit
  paths and any future non-opt-in scoped lane.

This document RECOMMENDS **Option A**: the cost is a single snapshot, and a
universal invariant is what stops the per-mode rediscovery. Ratification deferred
(see Compatibility).

Empirically (§7), no live consumer today wires a *non-opt-in* whole-tree repair:
`amarbel-llc/eng`'s only whole-tree repair (`doppelgang-flake`) opts into
`restage-repair-outputs`, so the conformist#70 opt-in-scoped fold already
satisfies INV-2 for it. Option A is therefore **forward-looking robustness** —
it removes the latent `--commit`-with-explicit-paths violation and the coupling
of format-correctness to a staging flag *before* a non-opt-in lane appears — not
a fix for an active production defect. That is part of why its ratification can
defer.

### 6. The partially-staged lane (open)

The `--staged` partially-staged sub-lane (conformist#40) formats each
partially-staged file's STAGED blob in an isolated temp tree and restages it via
the object store, leaving unstaged hunks untouched. As implemented, this lane
runs the repair phase inside the temp tree (which contains only the partial
blobs) with no output observation, and discards any repair output — so it honors
neither RFC-0002's stage-mutation tiers nor INV-2. It thus diverges from the
fully-staged lane for the same configuration.

**Options:**

- **Option A — documented carve-out (recommended).** Specify that the
  partially-staged lane runs the format phase over the staged blobs ONLY, and
  runs neither whole-tree repair semantics nor the stage-mutation tiers; a
  whole-tree codegen-repair linter has no effect on a purely partially-staged
  commit. Rationale: blob isolation is a deliberate safety property (#40), and
  whole-tree repair semantics over an isolated single-blob temp tree are
  ill-defined (the repair cannot see the rest of the tree). Make the boundary
  normative and documented rather than silently divergent.

- **Option B — hoist repair to the working tree.** Run the repair phase once over
  the real working tree (as the fully-staged lane does), observe `R`, and feed
  the partial lane's blob formatting from the post-repair tree. More uniform, but
  reintroduces the working-tree mutation the partial lane exists to avoid for
  unstaged hunks, and needs careful interaction with attribution.

- **Option C — refuse.** When a whole-tree repair-capable linter is configured
  AND a partially-staged file is present, refuse (exit 2) rather than silently
  skip the repair semantics.

This document RECOMMENDS **Option A** for now: it matches current behavior, keeps
the #40 safety property, and removes the *silent* divergence by making it a
specified, documented boundary. Ratification deferred.

### 7. Validation against live configuration (informative)

This model was checked against the `amarbel-llc/eng` global `conformist.toml`,
the config every eng repo and inheriting child repo uses, and the canonical
whole-tree-repair consumer. Findings:

- **INV-2 subject is real and matches §3.1.** The sole whole-tree repair linter,
  `[linter.doppelgang-flake]` (`passes-files = false`, `repair-command =
  doppelgang lint --fix`, `includes = ["flake.nix", "flake.lock"]`,
  `restage-repair-outputs = true`), regenerates `flake.nix` — which
  `[formatter.nixfmt]` (`includes = ["*.nix"]`) matches — and `flake.lock`, which
  no configured formatter matches. This is the §3.1 worked example verbatim: INV-2
  pulls `flake.nix` into format-scope and correctly leaves `flake.lock` out.
- **conformist#70 was a reachable gap, not hypothetical.** The linter's own config
  comment states it depends on the #70 fold: "the byte-spliced flake.nix is
  nixfmt-normalised before it is restaged — without it the raw, unformatted --fix
  output would land in the commit." This is the §3 INV-1 property in production
  use.
- **No live non-opt-in whole-tree repair exists today.** `doppelgang-flake` is
  opt-in (tier 2); eng's other linters (`bats-at-test`, `bash-syntax`) declare no
  `repair-command`. The tier-3/4 codegen consumers RFC-0002 anticipated
  (purse-first's dagnabit/dewey lanes, purse-first#166) are not yet wired as
  conformist linters — `amarbel-llc/purse-first` has no `conformist.toml`, and
  cannot take conformist as a flake input (conformist MUST stay strictly upstream
  of purse-first; no cycle). The stage-mutation tiers and §5 Option A are thus
  *ahead of their consumers*, which is why §5/§6 defer rather than block.

This validation is both config-level (the wiring matches the model) and
end-to-end empirical. The §3 INV-1/INV-2 *behavior* is exercised by
`TestStagedFormatsRepairOutputsBeforeRestaging` against the real `--staged` code
path (Conformance Testing), and was additionally run against eng's **real
`flake.nix` + real `nixfmt`** (`nixfmt-rfc-style` 1.2.0): a stub whole-tree
repair reproducing doppelgang's trigger≠output shape — staged `flake.lock` as the
trigger, an unstaged `flake.nix` byte-spliced into a non-`nixfmt`-canonical state
and self-`git add`ed — was driven through `conformist --staged
--exit-zero-on-fix`, and the resulting **staged `flake.nix` blob was
`nixfmt`-clean** (formatted before restage), confirming INV-1/INV-2 hold against
the real formatter, not only a synthetic stand-in.

## Security Considerations

**Stage-mutation blast radius is unchanged.** This document constrains content
*normalization* (INV-1/INV-2) and phase *ordering*; it does not widen which paths
a run may stage. RFC-0002 §2's tier opt-ins remain the sole authority over
whether a repair output enters the index, and its default (tier 1, staged set
only) is unchanged. An implementation MUST NOT use INV-2 (formatting a repair
output) as a pretext to stage a path RFC-0002's tiers would not stage; formatting
a path in the working tree does not stage it.

**Whole-pass observation does not attribute.** §5's recommended single tree-state
delta is for *formatting* (a union), not staging. An implementation MUST NOT
stage paths discovered only by the whole-pass delta; staging still requires the
per-linter attribution of RFC-0002 §2.5. Conflating the two would let one
linter's repair output be staged under another linter's opt-in.

**Format-before-stage prevents committing un-normalized content.** INV-1 is itself
a safety property for hook lanes: a pre-commit or pre-merge repair hook whose
purpose is to guarantee conformant commits would, without INV-1, be able to
commit a repair's raw output (e.g. an unformatted byte-spliced `flake.nix`),
defeating the hook. The toolchain-hermetic guarantee (RFC-0002 Security
Considerations) is necessary but not sufficient for this; INV-1 is the other half.

**Arbitrary repair commands.** Unchanged from RFC-0001 §4 / RFC-0002: a
`repair-command` is an arbitrary program run with the author's privileges. This
document governs only which of its effects are normalized and in what order, not
what it may execute.

## Conformance Testing

Conformance tests for this specification live alongside conformist's command
tests (`cmd/`, run under `just test-go`), and SHOULD be re-expressed against
`bats-emo` binary injection (`require_bin CONFORMIST conformist`) when
conformist's CLI surface is specified for alternative implementations.

### Covered Requirements

| Requirement | Test | Description |
|-------------|------|-------------|
| §3 INV-1 / INV-2, `--staged` | `TestStagedFormatsRepairOutputsBeforeRestaging` | a whole-tree repair writing unformatted, formatter-matched output, run under `--staged`, lands formatted in the index (conformist#70) |
| §4 matrix, `--staged` stage ⊆ format | `cmd/staged_repair_test.go` (RFC-0002 tier suite) | repair outputs enter the index only per the tiers, and as formatted content |
| §3 INV-1, `--commit` (Option A, §5) | *(to add)* | a whole-tree repair's formatter-matched output committed by `--commit <explicit path>` lands formatted |
| §4 matrix, `check` runs no repair/stage | *(to add)* | `check` over a tree with a repair-capable linter mutates nothing |
| §6 partially-staged carve-out | *(to add)* | a whole-tree codegen-repair linter has the specified (Option A: no) effect on a purely partially-staged commit |

## Compatibility

**Relationship to RFC-0001 and RFC-0002.** This document is additive. RFC-0001
(§3 check/repair modes, §4 `[linter.<name>]` and `passes-files`) and RFC-0002
(the stage-mutation tier model and the conformist-module interface) remain in
force unchanged. This document specifies the orthogonal phase-ordering and
scope-composition axis they both presume but neither states. Where RFC-0002 §2
governs *which* repair outputs are staged, this document's INV-1 governs that
they are *formatted first*, and INV-2 governs that the format phase *sees* them.

**conformist#70 partially implements §3.** The `formatScope` fold
(conformist#70) implements INV-1/INV-2 for the `--staged` fully-staged lane,
scoped to opt-in linters' outputs. Adopting §5 Option A would generalize that to
the full repair-output set and to `--commit`; adopting §6 Option A would document
the partially-staged carve-out. No behavior already shipped is invalidated.

**Backwards compatibility.** INV-1/INV-2 can only *add* formatting to a path that
would otherwise be staged un-normalized; they never stage a new path (that
remains RFC-0002's tiers) and never skip formatting a path that is already
formatted. A config that triggers no repair outputs (`R = ∅`) is unaffected:
format-scope reduces to the input paths exactly as today.

**Deferred ratification.** §2–§4 are the actionable core. §5 (universal
observation) and §6 (partial-stage lane) are exploratory forks; their
ratification, and any consequent implementation work, are tracked as follow-up
issues (see References). Until ratified, the shipped behavior (conformist#70's
opt-in-scoped fold, and the partial lane as-is) stands.

## References

### Normative

- [RFC 2119] — Key words for use in RFCs to Indicate Requirement Levels.
- [RFC-0001] — `docs/rfcs/0001-linter-support-and-check-repair-modes.md`: the
  `[linter.<name>]` section, check/repair modes, and `passes-files` semantics.
- [RFC-0002] — `docs/rfcs/0002-conformist-module-and-stage-mutation.md`: the
  stage-mutation tier model (the stage phase's capability sub-model) and the
  conformist-module interface.

### Informative

- conformist#70 — the `--staged` format-before-restage gap that motivates INV-1
  and is its first (opt-in-scoped) implementation.
- conformist#55 / #56 / #57 — the stage-mutation tiers 2/3/4 (RFC-0002), the
  point-fixes that share this document's unmodeled axis.
- conformist#67 — the `--commit` conflict-marker guard, a stage-phase policy.
- conformist#44 / #45 — the linter-watch vs formatter-exclude interaction (the
  repair/format scope-vs-excludes facet of axis 1).
- conformist#66 — declarative per-hook membership (a new column for the §4
  matrix).
- conformist#40 — the `--staged` partial-stage blob isolation (the §6 lane).
- amarbel-llc/eng `conformist.toml` — the live global config validated in §7;
  `[linter.doppelgang-flake]` is the canonical INV-2 subject. Wired by eng#205.
- amarbel-llc/purse-first#166 — the anticipated dagnabit/dewey codegen-repair
  consumers of RFC-0002's tiers, not yet wired as conformist linters (§7).
