---
status: proposed
date: 2026-07-28
---

# flakeclobber: Destructive flake.nix Edits for Fleet Migration

## Abstract

This document specifies **flakeclobber**, a separate binary that performs
targeted destructive edits to `flake.nix` files to migrate a fleet of repos
from the `pkgs.just` toolchain entry to a hermetic `just-us` flake input.
flakeclobber is intentionally separate from the `conform` command — it is a
one-shot operational sweep tool, not a composable subcommand. It shares the
shallow-Nix PEG infrastructure from `flakeedit` via a new `flakeparse`
sub-package, generalizes the existing `splice` model to cover first-class
replacements and deletions, and enforces a narrower invariant than flakeedit:
it operates only on spans identified by operator-supplied migration entries.

## Background

`cmd/conform/flakeedit` wires a repo's `flake.nix` into conformist via
targeted in-place splices. Its load-bearing invariant (from the package doc) is
**it never clobbers an attr it did not write**. Every edit is an insertion at a
computed splice point; the only replacement is the opt-in `--force-formatter`
path, and even that replaces only the value conformist itself would have written.

Adding `just-us` support to conformist requires extending the `eachDefaultSystem`
wiring in two ways:

1. **Additive:** a `just-us` flake input, a `justPkg` let binding, and the
   `just-us` argument in the outputs destructuring.
2. **Destructive:** replacing `pkgs.just` in an existing
   `devShells.default.packages` list with `justPkg`.

The additive changes fit flakeedit's insertion model. The destructive removal
does not; flakeedit has no mechanism to remove a list element.

Additionally, when `justPkg` is added to `conformistLetNames` (the idempotency
sentinel), repos wired by the previous conformist score 3-of-4 names. The
`Apply` switch at `cmd/conform/flakeedit/flakeedit.go:126` treats partial
presence as a foreign collision and returns `ErrUnrecognized`. The repos that
most need the migration are exactly the ones flakeedit refuses to touch.

## Premise Check

The task brief states "this worktree has uncommitted flakeedit changes adding
just-us support." At the time of writing (2026-07-28) the worktree is clean and
`conformistLetNames` contains three names (`conformistPkg`, `eval`,
`impureEval`). The `justPkg` sentinel extension described above is a planned
future change, not a current one. This document is written against that future
state — specifically the state where `justPkg` has been added to
`conformistLetNames` and is triggering `ErrUnrecognized` on already-wired repos.
If that extension is not yet in place, the flakeedit refusal path is not active,
but the design remains correct: the destructive removal alone is sufficient
reason to keep flakeclobber separate from flakeedit.

The brief also states "there are FDR/ADR/RFC directories in `docs/`." Only
`docs/rfcs/` exists. This document uses the RFC format.

## Decision: Separate Binary

flakeclobber lives at `cmd/flakeclobber/`, not as a `conform` subcommand and
not invoked by `conform`. Keeping it separate means:

- No opt-in flag a developer can accidentally pass during routine `conform` use.
- No code path by which a repo's CI gate or pre-commit hook triggers a
  migration.
- The binary can be retired after the fleet sweep without touching `conform`'s
  source or help output.

The established precedent is eng's `bin/update-nix-repos.bash` family — tools
that iterate repos in DAG order and drive each through `sc run`. flakeclobber
is the per-repo surgery step those scripts call.

## Design

### 1. The Shared Layer: `cmd/conform/flakeparse`

Both flakeedit and flakeclobber need the PEG parse infrastructure and
span-finding logic currently embedded in `cmd/conform/flakeedit/`. This logic
is extracted into a new package, `cmd/conform/flakeparse`.

**What moves to `flakeparse`:**

- The PEG grammars (`nix.peg`, `outputs.peg`) and their `//go:embed` vars
- Grammar entry constants (`nixEntry`, `outputsEntry`)
- `ErrUnrecognized`
- `compileMatcher()` and the navigation helpers (all unexported): `nodeName()`,
  `childNamed()`, `firstSequence()`, `topAttrSetSequence()`, `bindingKeyVal()`,
  `keyValPath()`, `attrPathSegments()`, `valueItems()`, `soleGroup()`
- Source-location helpers (exported): `LineStart()`, `LineIndent()`,
  `OnlyBlankBefore()`; `TokenIndex()` and `IsNixIdentChar()` (exported, new)
- The `Splice` type (exported) and its `ApplyTo()` method
- `InputsAttrSet` (exported) and `FindInputsAttrSet()`, along with
  `ScanBlockKeys()`
- `ParsedOutputs` (exported), `parseOutputs()`, `outputsValueSpan()`
- `ListSplice` (exported), `InnerStart()`, and `findPackagesList()`
- `ValueRange` (exported)
- Binding helpers (unexported): `collectBindings()`, `bindingPaths()`, `bindingNames()`,
  `identifiers()`, `bindingValue()`, `isBracketGroup()`, `packagesAssignment()`

**What stays in `flakeedit`:**

- `Apply()`, `EditReport`, `Options`
- `conformistLetNames`, `conformistDeps`, `devShellPackages`
- `inputsSplice()`, `letSplice()`, `returnSplice()`, `devShellMergeSplice()`,
  `returnAttrs()`, `beforeCloser()`, `dottedParent()`

**Existing tests stay green without changes.** `flakeedit_test.go` is package
`flakeedit_test` and exercises only exported symbols: `Apply()`, `Options`,
`EditReport`, and `ErrUnrecognized`. `Apply()` and `Options` remain in
`flakeedit`. `ErrUnrecognized` moves to `flakeparse` but is re-exported from
`flakeedit` so callers and tests compile unchanged:

```go
// cmd/conform/flakeedit/flakeedit.go
var ErrUnrecognized = flakeparse.ErrUnrecognized
```

All navigation internals that move are unexported in the existing tests; the
refactoring is invisible to `package flakeedit_test`.

### 2. The Splice Model

The `Splice` type (in `flakeparse`) unifies all edit operations:

```
type Splice struct {
    Offset int
    End    int
    Text   string
}
```

Semantics, unchanged from the current `splice` type in flakeedit:

- **Insert**: `End == 0` (or `End == Offset`), `Text` non-empty → insert `Text`
  at `Offset`.
- **Delete**: `End > Offset`, `Text == ""` → remove bytes `[Offset, End)`.
- **Replace**: `End > Offset`, `Text` non-empty → replace `[Offset, End)` with
  `Text`.

`ApplyTo()` already handles all three via the `end > offset` branch. No semantic
change is required.

**Ordering and correctness.** Splices are sorted by descending `Offset` before
application, so earlier offsets stay valid after each application. This reasoning
holds for deletions: a deletion at offset X reduces the file's byte count, but
since X is processed before any Y < X, Y's offset was recorded in the original
source and remains correct.

**The instability trap.** `sort.Slice` is not stable. Two splices at the same
`Offset` have undefined relative order. The current flakeedit avoids this by
construction (no two edits target the same offset). flakeclobber's migration
entries must satisfy the same constraint by design: each entry targets a
distinct span. This is a caller obligation, not enforced by the sort. The
constraint must be documented in `flakeparse`. If a future migration requires two
splices at the same offset, the sort must be replaced with `sort.SliceStable`
plus a stable secondary comparator on `End` or entry index.

### 3. flakeclobber's Guarantee

flakeedit's invariant: **never clobbers an attr it did not write.**

flakeclobber's invariant: **operates only on text spans matched by
operator-supplied migration entries; refuses when the file shape is unrecognized
or a matched span is not found in an expected state.**

**Dry-run by default.** Without `--apply`, flakeclobber prints a plan of what
it would change and exits 0 without modifying the file. This is the primary
safety check before running a fleet sweep.

**Shape refusal.** When the file does not match the recognized
`utils.lib.eachDefaultSystem` shape (`ErrUnrecognized`), flakeclobber exits
non-zero with a descriptive error. It does not silently skip. The recognized
shape is not widened (conformist#65 tracks that separately).

The fleet sweep wrapper treats non-zero exit as "needs manual attention," logs
the failure, and continues to the next repo. Silent failure would hide repos
requiring hand-migration.

**Already-migrated refusal is a no-op.** When all migration entries are already
satisfied (the file is fully migrated), flakeclobber exits 0. Dry-run output
reads the list of satisfied entries. This is the idempotent case, not an error.

**N/A is a no-op.** When neither the old element nor the new element is present
in the list, the migration simply does not apply to this file (e.g., a repo that
never used `pkgs.just`). This is not an error. N/A entries are not counted
toward satisfied entries and do not affect the partial-state check.

**Partial state is an error.** If some entries are satisfied and others are
pending (e.g., `justPkg` is already in the list but `pkgs.just` is also still
present), flakeclobber refuses and exits non-zero. Partial application is not
attempted; the operator must inspect and resolve manually.

**Parse verification.** After building the candidate output bytes (when
`--apply` is set), flakeclobber pipes them through `nix-instantiate --parse`
before writing to disk. A migration that produces syntactically invalid Nix is
rejected (file unchanged, exit 1). This closes the same class of bug that
`just verify-flakeedit-parse` guards against.

### 4. The Migration Interface

Migrations are supplied at call time via `--old` and `--new` flags rather than
a compiled-in table. Each `--old`/`--new` pair is one substitution. `--new` may
be omitted or empty to delete the matched element.

```
flakeclobber --old pkgs.just --new justPkg [--apply] <file>...
```

**Why flags instead of an in-code table?** The initial RFC draft considered a
`ListElementMigration` struct with `ID`, `Description`, and `FindList func`
fields compiled into the binary. The flags approach was chosen instead:

- A fleet sweep is a shell script that calls flakeclobber with fixed flags; the
  script is the reviewable migration record, audited at the same PR as the sweep.
- A compiled-in table would require rebuilding the binary for each new migration.
  Flag-driven substitutions can be pipelined (multiple `--old`/`--new` pairs in
  one invocation) without a recompile.
- The `FindList` abstraction (§4 original draft) was designed to support multiple
  target lists. In practice, the only migration target is
  `devShells.default.packages`, which is hardcoded. Generalizing was YAGNI.

**What flakeclobber targets.** The scope is **list-element replacement** within
the `devShells.default.packages` list, which is the only destructive mutation
required by the fleet migration. It does NOT:

- Add inputs, outputs arguments, or let bindings (those are additive and can be
  handled by `conform` once the destructive part is done and a repo reaches 4-of-4
  `conformistLetNames`).
- Edit any structure other than the `devShells.default.packages` list.

**Scope of the initial fleet sweep (`pkgs.just` → `justPkg`):**

The sweep script handles ALL four parts of the migration:

1. **Destructive (flakeclobber):** `pkgs.just` → `justPkg` in packages list.
2. **Additive (conform or manual):** `just-us` input, `justPkg` let binding,
   `just-us` outputs arg — these need to be applied to repos at 3-of-4 names
   before or after flakeclobber. Currently tracked as a gap: flakeedit refuses
   3-of-4 repos entirely. The sweep operator must apply additive edits by hand
   or via a separate tool until that gap is closed (tracked as conformist#N).

**Finding the element span in the source.** Given `ListSplice{CloseOff, Inner}`:

- `Inner` is the full text of the list including brackets, so `Inner[0] == '['`
  and `Inner[len(Inner)-1] == ']'`.
- The absolute source offset of `[` is `CloseOff - len(Inner) + 1`.
- A position `i` within `Inner` maps to source offset
  `(CloseOff - len(Inner) + 1) + i`.

To locate `pkgs.just`: scan `Inner` for the needle as a complete token
(bounded by non-identifier chars). The resulting splice is a **Replace** covering
only the token span (`oldStart` to `oldEnd`), or a whole-line deletion when the
deletion case (`--new ""`) leaves the line otherwise blank.

The token boundary check uses `isIdentChar(r)` (identifiers, dotted attr-paths,
`_`, `-`, `'`). This prevents `pkgs.just` from matching inside `pkgs.just-more`
or `not-pkgs.just`.

### 5. Idempotency and Verification

**Element state categorization.** For each `--old`/`--new` pair and the current
packages list:

| State | Old present | New present | Action |
|---|---|---|---|
| `pending` | yes | no | apply replacement |
| `satisfied` | no | yes | idempotent skip |
| `N/A` | no | no | migration doesn't apply to this file |
| `conflict` | yes | yes | error: ambiguous state |

For deletion migrations (`--new ""`): satisfied when Old is absent; pending when
Old is present.

**Atomicity.** All-or-none within a single invocation:

- All pending → apply all; exit 0.
- All satisfied/N/A → exit 0, "nothing to do."
- Some satisfied + some pending → **partial state** → exit 1, no edits.
- Any conflict or unexpected state → exit 1, no edits.

**Parse verification.** In `--apply` mode, the candidate bytes are piped through
`nix-instantiate --parse /dev/stdin` before writing. If the parse fails, the
file is not written and the exit is 1.

**Fixture matrix for `just verify-flakeclobber-parse`** (a new recipe in the
`verify` lane, parallel to `verify-flakeedit-parse`):

| Fixture | Shape | Pre-migration state | Expected outcome |
|---|---|---|---|
| `old-wiring.nix` | eachDefaultSystem | pkgs.just in packages | migrate → parse-clean → exit 0 |
| `already-migrated.nix` | eachDefaultSystem | justPkg in packages (pkgs.just gone) | no-op → exit 0 |
| `unrecognized.nix` | forAllSystems / other | (any) | refuse → exit 1 → file unchanged |
| `no-devshell.nix` | eachDefaultSystem | no devShells.default packages list | error → exit 1 → file unchanged |
| `no-just.nix` | eachDefaultSystem | neither pkgs.just nor justPkg in packages | N/A → exit 0 → file unchanged |

Note: `verify-flakeclobber-parse` is not yet implemented. It is tracked as a
follow-up.

### 6. Sweep Invocation

The fleet sweep drives each of the ~34 sibling repos through a fixed sequence,
modelled on `bin/update-repo-in-session.bash`:

```bash
#!/usr/bin/env bash
# Per-repo step in the fleet sweep. Called by the outer DAG-order loop.
set -euo pipefail
repo="$1"

# 0. Additive wiring (just-us input, justPkg let binding, just-us arg).
#    This step currently requires manual editing or a separate tool for
#    repos at 3-of-4 conformistLetNames (see §4 scope note).

# 1. Destructive flake.nix surgery (pkgs.just → justPkg in packages list).
flakeclobber --apply --old pkgs.just --new justPkg "$repo/flake.nix"

# 2. Fetch the new input (adds just-us to flake.lock).
(cd "$repo" && nix flake update just-us)

# 3. Stage the changed files.
(cd "$repo" && git add flake.nix flake.lock)

# 4. Commit.
(cd "$repo" && git commit -m "chore: migrate devShells.default to just-us input")

# 5. Pre-merge gate (just = validate build test verify lint).
sc run "$repo"

# 6. Push / merge via spinclass.
```

flakeclobber is responsible only for editing `flake.nix` and validating the
result with `nix-instantiate --parse`. It does not run `nix flake update`,
commit, or push. Those steps belong to the sweep script.

## 7. Open Question: justfile-orphan-summary Timing

**Question.** Should enabling the `justfile-orphan-summary` linter in each
repo's `conformist.nix` be part of the same sweep, or a separate pass?

**Recommendation: two passes.**

Pass 1 (this migration): wire the `just-us` input, `justPkg` let binding, and
replace `pkgs.just` in devShells. Do **not** enable `justfile-orphan-summary`.

Pass 2 (separate, per-repo): enable `justfile-orphan-summary` in
`conformist.nix` after verifying the repo's justfile satisfies the rule.

**Reasoning.**

The just-us wiring is a pure dependency change. No pre-merge gate will fail
because of it: it adds an input, replaces a reference, and adds a let binding,
none of which affect any existing lint rule.

Enabling `justfile-orphan-summary` is new enforcement. The rule requires every
orphan recipe to carry an inline summary comment. Repos whose justfiles have
drifted from this requirement will fail the pre-merge gate for reasons unrelated
to the `just-us` wiring. This is already observed: dodder (`lint-grammar`,
`test-grammar`), hyphence, and smith deployed non-conformant orphan recipes
while a prior convention sweep was in flight.

Decoupling the passes means:

1. The fleet gets the `just-us` input in a single clean sweep without gate
   failures due to justfile drift.
2. `justfile-orphan-summary` can be enabled per-repo after the repo's justfile
   is audited and fixed, rather than blocking the wiring migration on all of them.
3. The sweep operator can clearly distinguish repos that failed due to structural
   issues (wrong shape, unexpected list content) from repos that merely have
   outstanding justfile debt.

A single combined pass risks stalling the fleet sweep on repos where the
justfile drift is non-trivial to fix, creating a false dependency between two
independent concerns.

## Interface

These are the actual types and functions as implemented.

```go
// Package flakeparse: shared PEG infrastructure

var ErrUnrecognized = errors.New(
    "flakeparse: flake.nix is not the recognized eachDefaultSystem shape",
)

type Splice struct { Offset, End int; Text string }

func (s Splice) ApplyTo(src []byte) []byte

// ListSplice locates a Nix list for in-place operations.
// Inner is the full source text of the list including brackets.
// CloseOff is the absolute source offset of the closing ']'.
type ListSplice struct { CloseOff int; Inner string }

// InnerStart returns the absolute source offset of Inner[0] (the '[').
func (ls ListSplice) InnerStart() int

type ValueRange struct { Start, End int }

type InputsAttrSet struct { /* ... */ }
func (i InputsAttrSet) TopLevelNames() map[string]bool

type ParsedOutputs struct {
    ArgInsertOff     int
    ArgIndent        string
    ArgNames         map[string]bool
    LetCloseOff      int
    LetIndent        string
    LetExisting      map[string]bool
    RetCloseOff      int
    RetIndent        string
    RetExisting      map[string]bool
    DevShellPackages *ListSplice
    FormatterValue   *ValueRange
}

func ParseFlake(src []byte) (InputsAttrSet, ParsedOutputs, error)

// Package flakeclobber: migration binary

// ListElementMigration is one substitution within the devShells.default
// packages list. Old is the token to find; New is the replacement (empty
// means deletion).
type ListElementMigration struct {
    Old string
    New string
}

type ClobberReport struct {
    Applied   []string // descriptions of applied migrations
    Satisfied []string // descriptions of already-satisfied migrations
}

func (r ClobberReport) Changed() bool { return len(r.Applied) > 0 }

// Clobber applies migrations to src and returns the rewritten source.
//
// Errors:
//   - flakeparse.ErrUnrecognized: not the recognized eachDefaultSystem shape
//   - ErrNoDevShell: no devShells.default packages list found
//   - ErrPartialState: some entries satisfied, some pending; no edits applied
//   - other non-nil: conflict (both old and new found) or operational failure
//
// src is always returned unchanged on any error.
func Clobber(src []byte, migrations []ListElementMigration) ([]byte, ClobberReport, error)
```

Exit codes:

```
0 — all files processed (changes applied, already migrated, or dry-run)
1 — one or more files could not be migrated (shape unrecognized,
    partial state, parse failure); non-failing files still processed
2 — operational error (bad flags, I/O failure)
```
