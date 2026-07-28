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
it operates only on spans identified by an explicit migration table.

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
- `newMatcher()` and the navigation helpers: `nodeName()`, `childNamed()`,
  `firstSequence()`, `topAttrSetSequence()`, `bindingKeyVal()`, `keyValPath()`,
  `attrPathSegments()`, `valueItems()`, `soleGroup()`
- Source-location helpers: `lineStart()`, `lineIndent()`, `onlyBlankBefore()`,
  `spliceAt()`
- The `Splice` type (exported) and its `ApplyTo()` method
- `InputsAttrSet` (exported) and `FindInputsAttrSet()`, along with
  `ScanBlockKeys()`, `IsAttrPath()`, `AttrPathBeforeEquals()`
- `ParsedOutputs` (exported), `ParseOutputs()`, `OutputsValueSpan()`
- `ListSplice` (exported) and `FindPackagesList()`
- `ValueRange` (exported)
- Binding helpers: `CollectBindings()`, `BindingPaths()`, `BindingNames()`,
  `Identifiers()`, `BindingValue()`, `isBracketGroup()`, `packagesAssignment()`

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

flakeclobber's invariant: **operates only on text spans matched by entries in
an explicit migration table; refuses when the file shape is unrecognized or a
matched span is not found in an expected state.**

The invariant is enforced by restricting the scope of each migration entry (§4):
entries target named, well-typed structures (list elements, not arbitrary
spans), and the tool refuses rather than guesses when the target is absent.

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
reads "nothing to do." This is the idempotent case, not an error.

**Partial state is an error.** If some entries are satisfied and others are not
(e.g., `justPkg` is in the let block but `pkgs.just` is still in the packages
list), flakeclobber refuses and exits non-zero. Partial application is not
attempted; the operator must inspect and resolve manually.

### 4. The Migration Table

A migration is a named set of entries, each targeting a specific element type.
The scope is intentionally limited to **list-element replacement**: find a bare
identifier on its own indented line within a known Nix list, and replace it.

```
type ListElementMigration struct {
    // ID is a stable, unique identifier used in dry-run and error output.
    ID string
    // Description is the human-readable dry-run line.
    Description string
    // FindList locates the target list within the parsed outputs.
    FindList func(out ParsedOutputs) (*ListSplice, bool)
    // OldElement is the exact text of the list element to remove.
    OldElement string
    // NewElement replaces OldElement. Empty means pure deletion.
    NewElement string
}
```

Why list-element scope? `pkgs.just` is a bare identifier on its own line in a
Nix list. Its span within the list's source text is unambiguous: scan for the
line matching `\s+pkgs\.just\s*`. An attrset binding or arbitrary expression
would require parsing the value to find its end — too much parser complexity
for a one-shot migration tool. List elements are visually and syntactically
simple, and the favourable property that `pkgs.just` is a bare identifier (not
a function application or let-in expression) is what makes this scope safe.

**The initial migration — `just-us-v1`:**

List-element migration:

```
ListElementMigration{
    ID:          "devshell-pkgs-just-to-justPkg",
    Description: "replace pkgs.just with justPkg in devShells.default packages list",
    FindList:    func(out ParsedOutputs) (*ListSplice, bool) {
                     return out.DevShellPackages, out.DevShellPackages != nil
                 },
    OldElement:  "pkgs.just",
    NewElement:  "justPkg",
}
```

Additive operations (these use the same insertion model as flakeedit):

- **Input**: `just-us.url = "git+https://code.linenisgreat.com/just-us.git";`
  with `just-us.inputs.nixpkgs.follows = "nixpkgs"` only when `nixpkgs` is
  already a top-level input name. This mirrors the conformist#83 fix: a follows
  aimed at a missing input fails eval; flakeclobber checks
  `InputsAttrSet.TopLevelNames()["nixpkgs"]` before adding the follows line.
- **Outputs arg**: `just-us,` spliced just after the opening `{`.
- **Let binding**: `justPkg = just-us.packages.${system}.default;` spliced just
  before `in`.

**Finding the element span in the source.** Given `ListSplice{CloseOff, Inner}`:

- `Inner` is the full text of the list including brackets, so `Inner[0] == '['`
  and `Inner[len(Inner)-1] == ']'`.
- The absolute source offset of `[` is `CloseOff - len(Inner) + 1`. This is
  cleanly encapsulated by a method `ListSplice.OpenOff() int`.
- A position `i` within `Inner` maps to source offset `OpenOff() + i`.

To locate `pkgs.just`: scan `Inner` for a line whose trimmed content is exactly
`pkgs.just`. If it appears more than once, fail loudly (ambiguous). If it is
absent but `justPkg` is present at the same position, the entry is
already-migrated → idempotent skip. If neither is found, the state is
unexpected → fail loudly.

The resulting splice is a **Replace** covering the full line (from the start of
its leading whitespace through the trailing newline) with the equivalent line
using `NewElement` and the same indentation.

### 5. Idempotency and Verification

**Idempotency per entry:**

| Entry type | Already-done signal | Action |
|---|---|---|
| List-element replace | `OldElement` absent, `NewElement` present | skip |
| Input addition | `just-us` in `InputsAttrSet.TopLevelNames()` | skip |
| Outputs arg | `just-us` in `ParsedOutputs.ArgNames` | skip |
| Let binding | `justPkg` in `ParsedOutputs.LetExisting` | skip |

All entries skipped → exit 0, "nothing to do."
Any entries skipped but not all → partial state → exit non-zero, no edits.

**Parse verification.** After building the candidate output bytes, flakeclobber
pipes them through `nix-instantiate --parse` before writing to disk. A migration
that produces syntactically invalid Nix is rejected (exit 2, file unchanged).
This mirrors `just verify-flakeedit-parse` and closes the same class of bug:
a splice that yields unparseable Nix is caught before it reaches a git commit.

**Fixture matrix for `just verify-flakeclobber-parse`** (a new recipe in the
`verify` lane, parallel to `verify-flakeedit-parse`):

| Fixture | Shape | Pre-migration state | Expected outcome |
|---|---|---|---|
| `old-wiring.nix` | eachDefaultSystem | conformistPkg/eval/impureEval in let; pkgs.just in packages; no just-us | migrate → parse-clean → exit 0 |
| `already-migrated.nix` | eachDefaultSystem | justPkg in let; just-us in inputs; justPkg in packages | no-op → exit 0 |
| `unrecognized.nix` | forAllSystems / other | (any) | refuse → exit 1 → file unchanged |
| `eng-inputs.nix` | eachDefaultSystem | igloo + nixpkgs-master inputs; no top-level nixpkgs | migrate without `just-us.inputs.nixpkgs.follows`; parse-clean |

The `eng-inputs.nix` fixture exercises the conformist#83 class: igloo-style repos
use `igloo` and `nixpkgs-master` as top-level inputs but have no top-level
`nixpkgs`. Adding `just-us.inputs.nixpkgs.follows = "nixpkgs"` would point a
follows at a missing input and fail eval. flakeclobber must skip that follows
line for any input dependency not present as a top-level name.

### 6. Sweep Invocation

The fleet sweep drives each of the ~34 sibling repos through a fixed sequence,
modelled on `bin/update-repo-in-session.bash`:

```bash
#!/usr/bin/env bash
# Per-repo step in the fleet sweep. Called by the outer DAG-order loop.
set -euo pipefail
repo="$1"

# 1. Destructive flake.nix surgery.
flakeclobber --apply "$repo/flake.nix"

# 2. Fetch the new input (adds just-us to flake.lock).
(cd "$repo" && nix flake update just-us)

# 3. Stage the two changed files.
(cd "$repo" && git add flake.nix flake.lock)

# 4. Commit (conformist --commit runs the full gate inline if desired,
#    but a plain commit is enough here; the gate runs at step 5).
(cd "$repo" && git commit -m "chore: migrate devShells.default to just-us input")

# 5. Pre-merge gate (just = validate build test verify lint).
sc run "$repo"

# 6. Push / merge via spinclass.
```

flakeclobber is responsible only for editing `flake.nix` and validating the
result with `nix-instantiate --parse`. It does not run `nix flake update`,
commit, or push. Those steps belong to the sweep script.

The `conformist.nix` change (enabling `justfile-orphan-summary`) is a separate
step not included in this sweep — see §7.

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

## Interface Sketch

These are type and function signatures to anchor the implementation; they are
not final API. All are subject to the usual Go style and review process.

```go
// Package flakeparse: shared PEG infrastructure (new package)

var ErrUnrecognized = errors.New(
    "flakeparse: flake.nix is not the recognized eachDefaultSystem shape",
)

type Splice struct{ Offset, End int; Text string }

func (s Splice) ApplyTo(src []byte) []byte

// ListSplice locates a Nix list for in-place operations.
// Inner is the full source text of the list including brackets.
// CloseOff is the absolute source offset of the closing ']'.
type ListSplice struct{ CloseOff int; Inner string }

func (ls ListSplice) OpenOff() int { return ls.CloseOff - len(ls.Inner) + 1 }

type ValueRange struct{ Start, End int }

type InputsAttrSet struct { /* existing fields, exported */ }
func FindInputsAttrSet(tree langlang.Tree, src []byte) (InputsAttrSet, bool)
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

func OutputsValueSpan(tree langlang.Tree) (start, end int, ok bool)
func ParseOutputs(src []byte, base, end int) (ParsedOutputs, bool)

// Package flakeclobber: migration binary (new)

type ListElementMigration struct {
    ID          string
    Description string
    FindList    func(out flakeparse.ParsedOutputs) (*flakeparse.ListSplice, bool)
    OldElement  string
    NewElement  string
}

type ClobberReport struct {
    Applied []string // entry IDs applied
    Skipped []string // entry IDs that were already satisfied
}

// Clobber applies the migration table to src and returns the rewritten
// source. It returns ErrUnrecognized when the file is not the recognized
// shape, and a non-nil error on partial state or unexpected element state.
// src is returned unchanged on any error.
func Clobber(src []byte, mig []ListElementMigration) ([]byte, ClobberReport, error)
```
