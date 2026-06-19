---
status: exploring
date: 2026-06-19
---

# The conformist Module Interface and the Pre-commit Stage-Mutation Contract

## Abstract

This document specifies how conformist is wired into git pre-commit validation
and repair, and the contract by which a configured run may mutate the git index
("the stage"). It defines a **stage-mutation capability model** — a tiered,
per-linter set of opt-ins by which a repair may reformat staged files, restage
the tracked files its repair regenerates, stage brand-new repair outputs, and
stage repair-driven deletions — each tier more powerful and each off by default.
It also defines what a **conformist module** is: the unit a consumer declares
once to obtain a toolchain-hermetic pre-commit/repair hook, and who evaluates it.
The goal is to collapse the several overlapping Nix wrapper outputs conformist
ships today into one coherent interface, and to give codegen-repair lanes (which
regenerate files other than the staged ones) a first-class place in the staged
hook.

## Introduction

conformist runs as a git pre-commit hook via `conformist --staged
--exit-zero-on-fix` (RFC 0001 defines the check/repair modes; this document
concerns the *staged* mode and the Nix glue around it). The hook formats the
files staged in the index, restages the formatted content, and exits 0 so the
caller's commit proceeds with conformant content. A sibling repair hook
(`conformist --commit --amend --exit-zero-on-fix`) drives a spinclass pre-merge
repair lane.

Two problems motivate this specification.

### Problem 1 — the staged restage scope strands codegen-repair outputs

`--staged` restages only files that were **already in the index**. This is
correct for formatters (which rewrite the very files being committed) and for
per-file linters. But a **whole-tree codegen-repair linter** (`passes-files =
false` with a `repair-command`, per RFC 0001 §4) regenerates files *other than*
the staged ones. Concrete shape: editing a staged `internal/foo.src` makes a
generated `pkgs/foo.generated` facade stale; the lane's `repair-command`
regenerates the facade on disk, but that path was never staged, so the staged
hook leaves it modified-but-unstaged and the commit lands stale. The same shape
recurs in every codegen-repair lane (a `dagnabit`-facade export, a
`tommy`-codegen `*_tommy.go` regen, a `dewey`-reposition package move). The
conformist#55 fix (see Compatibility) introduces the first tier of the model
specified here; this document generalizes it.

### Problem 2 — the Nix glue is several overlapping wrappers

To get a *toolchain-hermetic* hook (one that does not silently skip file types
whose formatter is absent from the author's PATH — see Security Considerations),
conformist's Nix library today exposes several overlapping outputs:

- `build.wrapper` — the `nix fmt` repair entrypoint.
- `build.preCommit` — `conformist-pre-commit` = `--staged --exit-zero-on-fix`.
- `build.repair` — `conformist-repair` = `--commit --amend --exit-zero-on-fix`.
- `build.check` — a flake check running `conformist check`.
- `lib.wrapWithToolchain` — a *non-module* wrapper carrying the toolchain on
  PATH, for a repo with a hand-written `conformist.toml` rather than the module.

`build.{wrapper,preCommit,repair}` share a single body (`mkHookWrapper`) but are
declared and wired separately; `wrapWithToolchain` is a second, parallel wrapper
family for the non-module path. A consumer must know which output to reference
in which slot (`nix fmt`, sweatfile `pre-commit`, sweatfile `repair`), and the
two families can drift. This document specifies a single module interface that
subsumes these, so a consumer declares intent once.

This specification defines the configuration interface and behavioral contract.
It is **exploratory**: it lays out the stage-mutation model in full and presents
the module-shape options (status quo polish vs. a conformist-native format vs. a
hybrid) with a recommendation, but ratification of the module shape is deferred
pending review (see Compatibility / Open Questions).

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### 1. Terminology

- **Stage / index** — the git index, the set of changes a commit will record.
- **Staged set** — the toplevel-relative paths that have a staged (index)
  change at the moment the hook runs.
- **Codegen-repair linter** — a whole-tree linter (`passes-files = false`, RFC
  0001 §4) with a `repair-command` that regenerates files other than its
  trigger inputs.
- **Repair output** — a path written, created, or deleted by a linter's
  `repair-command` during a run.
- **Status delta** — the difference between `git status` snapshots taken
  immediately before and after a single linter's repair, attributing the
  changed paths to that linter's `repair-command`.
- **Stage mutation** — any change a conformist run makes to the index: restaging
  reformatted content, restaging a repair output, staging a new file, or staging
  a deletion.
- **conformist module** — the declarative unit a consumer evaluates to obtain a
  configured, toolchain-hermetic `conformist` invocation for `nix fmt`, the
  pre-commit hook, the repair hook, and the flake check.

### 2. The stage-mutation capability model

A `--staged` run MUST default to the narrowest stage mutation that is safe
without configuration, and MUST require an explicit per-linter opt-in for each
broader capability. The capabilities form four tiers:

| Tier | Capability | Opt-in | Status |
|------|------------|--------|--------|
| 1 | Reformat staged files and restage the formatted content | none (always on) | specified by RFC 0001 + #25/#40 |
| 2 | Restage a linter's **modified tracked** repair outputs | `restage-repair-outputs` | specified here; implemented by #55 |
| 3 | Stage a linter's **newly created (untracked)** repair outputs | `restage-repair-outputs` + `stage-new-outputs` | specified here; not yet implemented |
| 4 | Stage a linter's repair **deletions** | `restage-repair-outputs` + `stage-deleted-outputs` | specified here; not yet implemented |

#### 2.1 Tier 1 — reformat and restage staged files (default)

A `--staged` run MUST format the staged content and restage it, scoped to the
staged set, exactly as RFC 0001 and conformist#25/#40 specify. A fully-staged
file is formatted in the working tree and `git add`ed; a partially-staged file
has its staged blob formatted in isolation and restaged via the object store. No
configuration enables this tier; it is the baseline.

A run MUST NOT, at tier 1, restage any path outside the staged set. This is the
safety property that makes the hook usable: an author's unrelated unstaged edits
are never swept into the commit.

#### 2.2 Tier 2 — restage modified tracked repair outputs

A linter MAY set `restage-repair-outputs = true`. This key has effect ONLY on a
linter that is whole-tree (`passes-files = false`) AND has a `repair-command`;
on any other linter it MUST be ignored.

When set, a `--staged` run MUST restage the **tracked** paths that this linter's
`repair-command` modified, even when those paths were not in the staged set. The
run MUST attribute modified paths to the linter via a status delta taken around
that linter's repair, so that:

- a path already dirty before the linter's repair MUST NOT be attributed to it
  (it was not this repair's output); and
- a path another linter's repair modified MUST NOT be attributed to this linter
  (each opt-in linter's repair is snapshotted individually).

At tier 2 a run MUST restage only **tracked, modified** outputs. A brand-new
(untracked) output or a deletion MUST NOT be staged at this tier (those are
tiers 3 and 4). This bounds the blast radius: a tier-2 lane can only update
files git already tracks.

#### 2.3 Tier 3 — stage newly created repair outputs

A linter that already sets `restage-repair-outputs = true` MAY additionally set
`stage-new-outputs = true`. When both are set, a `--staged` run MUST stage
brand-new (untracked) files this linter's `repair-command` created, in addition
to the tier-2 modified outputs.

Because staging untracked files is more dangerous than restaging tracked ones (a
buggy or over-broad codegen command could sweep in unintended new files), this
tier MUST be a distinct opt-in from tier 2, and a run MUST NOT stage untracked
outputs under tier 2 alone. An implementation detecting tier-3 outputs MUST
include untracked paths in its status snapshots for the linter in question
(e.g. `git status --untracked-files=all` scoped to that repair), and MUST still
attribute by delta so pre-existing untracked files are not swept in.

`stage-new-outputs` MUST be ignored unless `restage-repair-outputs` is also
true.

#### 2.4 Tier 4 — stage repair deletions

A linter that already sets `restage-repair-outputs = true` MAY additionally set
`stage-deleted-outputs = true`. When both are set, a `--staged` run MUST stage
the **deletions** this linter's `repair-command` performed (e.g. a package-move
codegen that removes a relocated file), so the deletion is part of the commit
rather than left as an unstaged removal.

This tier MUST be distinct from tiers 2 and 3 because staging a deletion removes
a path from the commit's tree, which is the most destructive stage mutation. A
run MUST attribute deletions by status delta as in tier 2.

`stage-deleted-outputs` MUST be ignored unless `restage-repair-outputs` is also
true.

#### 2.5 Attribution mechanism (normative)

For every tier 2–4 opt-in linter, an implementation MUST run that linter's
`repair-command` under an individual before/after `git status` snapshot, and MUST
restage/stage only the paths the delta attributes to that linter, filtered to
the tier's permitted change kinds (modified / created / deleted). An
implementation MUST NOT run an opt-in linter's repair interleaved with another
linter's repair in a way that conflates their deltas. A run MUST NOT depend on a
linter declaring its output paths in configuration; attribution is by observed
filesystem effect, because a `repair-command` is an arbitrary external program.

### 3. Configuration keys

The following keys are added to the `[linter.<name>]` table (RFC 0001 §4). All
are booleans defaulting to `false`.

- `restage-repair-outputs` — tier 2. Effective only on a whole-tree linter with
  a `repair-command`.
- `stage-new-outputs` — tier 3. Effective only when `restage-repair-outputs` is
  also true.
- `stage-deleted-outputs` — tier 4. Effective only when `restage-repair-outputs`
  is also true.

An implementation MUST accept these keys in `conformist.toml` and via the Nix
module's freeform linter submodule. An implementation MAY warn when a key is set
on a linter where it has no effect (e.g. `restage-repair-outputs` on a per-file
linter), but MUST NOT error.

Example (tier 2, the #55 case):

```toml
[linter.dagnabit-facade]
command = "dagnabit-check"
repair-command = "dagnabit export"
includes = ["internal/**/*.go"]
passes-files = false
restage-repair-outputs = true
```

### 4. The conformist module interface

A conformist module is the declarative unit from which a consumer obtains a
configured `conformist`. It MUST yield, from a single declaration of formatters,
linters, and excludes:

1. a generated `conformist.toml` whose every tool `command` is resolved to an
   absolute (store) path, so the configured invocation is toolchain-hermetic;
2. a single hook entrypoint, parameterized by mode, covering:
   - `nix fmt` (repair over the working tree),
   - the pre-commit hook (`--staged --exit-zero-on-fix`),
   - the repair hook (`--commit --amend --exit-zero-on-fix`); and
3. a read-only flake check (`conformist check`).

The module interface MUST collapse today's separate `build.wrapper`,
`build.preCommit`, and `build.repair` outputs into one mode-parameterized
factory so a consumer references one thing per slot and the modes cannot drift
in their wrapper body. The interface SHOULD also subsume the non-module
`wrapWithToolchain` path so a repo with a hand-written `conformist.toml` obtains
the same hermetic guarantee without a second, parallel wrapper family.

A consumer MUST obtain its hook from its OWN module evaluation, not from
conformist's own packaged hook (which is built from conformist's config, not the
consumer's) and not from a bare `conformist --staged` string (which is not
toolchain-hermetic — see Security Considerations).

#### 4.1 Who evaluates the module — options

This is the open design axis. Three options, with the recommendation in §4.2:

- **Option A — Nix evaluates; conformist consumes the generated config (status
  quo, polished).** The module stays a Nix module (the home-manager/nixos
  shape). Nix generates the store-pinned `conformist.toml` and the single hook
  factory; conformist consumes them unchanged. Lowest risk. Removes the wrapper
  duplication (Problem 2) but keeps the dependency on a Nix evaluation to produce
  a working hook.

- **Option B — conformist evaluates a native module format itself.** conformist
  grows a module/preset system beyond raw `conformist.toml` (importable rosters,
  a declared hook section), resolved by conformist at runtime with no Nix
  evaluation required to get a working hook. This decouples the hook story from
  Nix, but raises an unresolved crux: conformist cannot, by itself, resolve tool
  paths *hermetically* without a Nix-provided store closure. A native format
  could express intent, but the toolchain-hermetic guarantee (Security
  Considerations) would still require an external provider. Documented for
  completeness; likely a non-starter for the hermetic property alone.

- **Option C — hybrid (recommended).** Nix remains the hermetic toolchain
  provider AND the generator of the store-pinned config, but conformist gains
  first-class *module* semantics in its own configuration surface: importable
  presets (the `eng` roster expressed in conformist's config vocabulary) and a
  declared `[hook]` section describing the pre-commit and repair modes, so a
  consumer expresses intent once and a thin generated wrapper carries the store
  toolchain. This keeps the hermetic guarantee (Nix-provided closure) while
  giving conformist a coherent, single module surface rather than several
  Nix outputs.

#### 4.2 Recommendation

This document RECOMMENDS Option C together with the tier-2-through-4
stage-mutation model. Rationale: the hermetic guarantee is load-bearing and only
Nix supplies it today, so Option B's full decoupling is premature; Option A
solves Problem 2 but leaves the module surface as a set of Nix outputs rather
than a conformist-level concept. Option C collapses the wrapper families (Problem
2) AND gives conformist a native notion of "a module" (the user's stated goal)
without abandoning the hermetic closure.

Ratification of §4 is deferred (status: exploring). §2–§3 (the stage-mutation
model and its config keys) are the immediately actionable part; tier 2 is
implemented by conformist#55.

## Security Considerations

**Toolchain-hermetic hooks (the silent-skip hazard).** conformist resolves each
tool's `command` from PATH for a bare-name command. A bare `conformist --staged`
run under an author's shell that lacks a formatter (gofumpt, nixfmt, …) silently
skips that file type — the commit proceeds *unformatted* with a zero exit, which
is a correctness/trust hazard for a hook whose entire purpose is to guarantee
conformant commits. The module interface (§4) mitigates this by pinning every
`command` to an absolute store path in the generated config; any module-derived
hook is therefore hermetic. A non-module, hand-written-config hook MUST carry its
toolchain on PATH (the `wrapWithToolchain` role) to obtain the same guarantee.

**Stage mutation blast radius.** Each capability tier (§2) widens what a run may
write into the commit. The default (tier 1) restages only the staged set, so an
author's unrelated unstaged edits are never committed. Tiers 2–4 each require an
explicit per-linter opt-in precisely because they let a `repair-command`'s output
enter the commit; an over-broad or buggy codegen command under tier 3
(`stage-new-outputs`) could otherwise sweep unintended new files into history.
The attribution-by-delta requirement (§2.5) bounds each opt-in linter to the
paths *it* changed, so enabling a tier on one lane cannot cause another lane's
(or the author's) changes to be staged. An implementation MUST NOT widen any
tier's default to on.

**Arbitrary repair commands.** A `repair-command` is an arbitrary external
program run with the author's privileges at commit time. This is inherent to the
linter model (RFC 0001 §4) and not introduced here; the stage-mutation tiers do
not grant a `repair-command` any capability it did not already have to write the
filesystem — they only govern which of its effects reach the index.

**Signing under the repair hook.** The repair hook (`--commit --amend`) re-signs
HEAD; a locked commit-signing agent fails the amend rather than producing an
unsigned commit. This is the desired failure mode (no silent unsigned commits).

## Conformance Testing

Conformance tests for the stage-mutation model live alongside conformist's
existing command tests (`cmd/`, run under `just test-go`). Tier 2 is covered by
`cmd/staged_repair_test.go`:

| Requirement | Test | Description |
|-------------|------|-------------|
| §2.2 restage modified tracked outputs | `TestStagedRestagesRepairOutputs` | an opt-in codegen-repair linter's regenerated facade is restaged though never staged |
| §2.2 / §2.5 per-linter attribution + opt-in gate | `TestStagedRepairOutputsOptInGate` | a non-opt-in linter's output is left unstaged; only the opt-in linter's output is restaged |

Tiers 3 and 4 are not yet implemented; their conformance tests are to be added
when they are.

When conformist's CLI surface is later specified against `bats-emo`, these tiers
SHOULD be re-expressed as binary-injected bats conformance tests so an
alternative implementation can run the same suite.

## Compatibility

**conformist#55 (tier 2).** The `restage-repair-outputs` key and tier-2 behavior
are implemented as the first increment of this model. The key defaults to false,
so existing configs are unaffected: a run without it behaves exactly as before
(tier 1 only). Tier 2 is therefore backwards compatible — it can only *add*
restaging for a linter that explicitly opts in.

**Tiers 3 and 4.** `stage-new-outputs` and `stage-deleted-outputs` are specified
here but not yet implemented (tracked as conformist#56 and conformist#57). Until
implemented, an implementation MUST ignore them (treating a tier-3/4 lane as
tier-2). When implemented, they remain default-false and gated behind
`restage-repair-outputs`, so adding them is backwards compatible.

**Module interface (§4).** The §4 module shape is exploratory and not yet
ratified; the current Nix outputs (`build.{wrapper,preCommit,repair,check}`,
`lib.wrapWithToolchain`) remain the supported interface until a successor RFC or
this RFC's promotion specifies the collapse and a migration path (tracked as
conformist#58). No existing consumer wiring is changed by this document.

## References

### Normative

- [RFC 2119] — Key words for use in RFCs to Indicate Requirement Levels.
- [RFC-0001] — `docs/rfcs/0001-linter-support-and-check-repair-modes.md`: the
  `[linter.<name>]` section, check/repair modes, and `passes-files` semantics
  this document builds on.

### Informative

- conformist#55 — the staged-restage gap that motivates tier 2 (implemented
  here).
- conformist#56 — tier 3 (`stage-new-outputs`), the follow-up for staging
  newly-created repair outputs.
- conformist#57 — tier 4 (`stage-deleted-outputs`), the follow-up for staging
  repair-driven deletions.
- conformist#58 — the §4 wrapper-collapse follow-up.
- conformist#25 / conformist#40 — the `--staged` lint-staged restage and its
  partial-stage blob-isolation semantics (tier 1).
- conformist#24 / conformist#33 — `--commit` / `--commit --amend`, the
  status-delta fix-set detection this document's attribution mechanism mirrors.
- conformist#47 / conformist#51 / conformist#54 — `build.preCommit`,
  `wrapWithToolchain`, and `build.repair`: the overlapping wrappers §4 proposes
  to collapse.
- amarbel-llc/purse-first#166 — the consumer-side codegen-repair lanes
  (dagnabit/dewey) that need tiers 2–4.
