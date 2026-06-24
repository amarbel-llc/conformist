# conform: in-place flake.nix editing + repo-name version.env

- **Status:** approved (design); implementation in progress
- **Date:** 2026-06-24
- **Issue:** [#61](https://github.com/amarbel-llc/conformist/issues/61)
- **Related:** #42 (leave repo in final conformed state with zero user action)

## Problem

`conformist conform` has two scaffolding gaps against an *existing* repo:

1. **`flake.nix` is never edited in place.** When `flake.nix` exists, `conform`
   refuses to touch it and prints a "paste this into flake.nix" block instead.
   The user has to hand-wire the `conformist` input and the per-system outputs.
2. **`version.env` hardcodes `EXAMPLE`.** The scaffolded key is
   `EXAMPLE_VERSION` rather than `<REPO>_VERSION` derived from the actual repo.

The goal: `conform` should do **as much as possible automatically** for both
greenfield repos and repos that already have a flake (whether or not they
already reference conformist), supporting **in-place** edits.

## Survey: what eng flakes actually look like

Fetched `flake.nix` from all 21 active (non-fork, non-archived) first-party
amarbel-llc repos:

| Shape | Count | Notes |
|---|---|---|
| `utils.lib.eachDefaultSystem` + `let … in { }` | **20/21 (95%)** | flat-block `inputs`, multiline destructured outputs args |
| `raw` (forAllSystems/genAttrs) | 1 (igloo) | hand-rolled |
| `flake-parts` | **0** | nobody uses it |

- **100%** have a per-system `let … in { }` block; **100%** use flat-block
  `inputs`.
- **8/21 already reference conformist** (conformist, crap, cutting-garden, eng,
  madder, moxy, papi, purse-first) — so "already partially wired" is a common
  state the editor must handle idempotently.
- Edge cases that fall back to print-only: igloo (`raw`), eng (hybrid:
  `<modules> // eachDefaultSystem (…)`). ~8 repos use a `...` ellipsis in the
  outputs args (which makes the arg-injection step a no-op).

**Decision:** recognize **`eachDefaultSystem` + `let … in { }` only**. It covers
95% of real eng repos; flake-parts is YAGNI. Everything unrecognized →
print-only fallback (today's behavior).

## Approach

Build a **shallow Nix PEG editor**, modeled on and reusing
[doppelgang](https://github.com/amarbel-llc/doppelgang)'s `internal/0/nixedit`.
doppelgang parses `flake.nix` with a shallow Nix grammar (via the
`github.com/clarete/langlang/go` PEG runtime, v0.0.12), navigates to the
top-level `inputs` attrset, and splices bindings in **by byte offset**, leaving
the rest of the file byte-for-byte. Any file the grammar can't parse yields
`ErrUnparseable` and the caller falls back to print-only.

doppelgang only ever edits `inputs`. conformist needs **four** splice targets,
so we extend the grammar to descend *into* the `outputs` value and find the
`eachDefaultSystem(system: let … in { … })` call.

Alternatives rejected: regex/line-anchor injection (brittle — the reason
doppelgang built a PEG); a real Nix parser / `nix-instantiate --parse`
(AST round-trip reformats the whole file, killing byte-fidelity; needs nix at
runtime).

## Components

New package `cmd/conform/flakeedit` (conform-only concern; conformist uses flat
top-level packages, not doppelgang's `internal/0/` layout):

- **`nix.peg`** (embedded) — start from doppelgang's grammar verbatim (attrsets +
  opaque balanced-run values), extend to recognize a **lambda** (`{ args }: body`)
  and **function application** so the `eachDefaultSystem` call and its
  `(system: let … in { … })` argument can be located.
- **`walk.go`** — port doppelgang's CST helpers (`newMatcher`, `childNamed`,
  `keyValPath`, `firstSequence`, `spliceAt`, `lineIndent`/`afterSemicolon`,
  `valueItems`, `soleGroup`). Add navigators: `findOutputsLambda`,
  `findEachDefaultSystemCall`, `findLetBlock`, `findReturnAttrSet`.
- **`flakeedit.go`** — `Apply(src) (out []byte, report EditReport, err error)`.
  Runs the four splice passes; returns `ErrUnrecognized` to trigger print-only.

**Dependency:** add `github.com/clarete/langlang/go v0.0.12` (upstream, no
`replace` — same as doppelgang). Leaf PEG runtime, no purse-first cycle.

## The four splice targets + idempotency

Each pass detects-then-adds-only-what's-missing, so the 8 already-conformist
repos re-run as no-ops:

1. **`inputs`** — splice `conformist.url` + `nixpkgs.follows`/`utils.follows`.
   Verbatim doppelgang behavior. Skip any already bound.
2. **outputs arg set** — add `conformist` to `{ self, … }:` **only if absent and
   no `...` ellipsis**. Our injected bindings reference only `conformist` (plus
   the existing `pkgs`/`system`), so that's the only arg needed.
3. **per-system `let`** — splice `conformistPkg`/`eval`/`impureEval`, skipping
   any name already bound.
4. **return attrset** — splice `formatter`, `checks.formatting`,
   `packages.conformist-{impure-config,pre-commit,repair}`, `devShells.default`,
   skipping any attr already present.

**Conflict policy ("do as much as possible safely"):** when a target attr
already exists with *different* content (e.g. `formatter = pkgs.nixfmt;`, or a
populated `devShells.default`), **skip that one piece and emit it in a
"wire these by hand" tail** — never clobber. Everything non-conflicting still
gets edited in place.

## version.env naming

Add `git.OriginRepoName(dir)`: `git config --get remote.origin.url` → last path
segment minus `.git`; fall back to `filepath.Base(dir)`. Uppercase, `-`→`_`.
Turn `scaffold/version.env` into a template (replace `EXAMPLE`). Only matters
when the file is freshly written (existing `version.env` is still skipped).

## Exit codes, reporting, rollback

- `Result` gains `Edited []string` (per-target summary). Exit **3** when anything
  was **written or edited** (today: writes only); **0** when fully idempotent.
- Report distinguishes `wrote X` / `edited flake.nix (added: …)` / `kept X` /
  the `# add by hand` conflict + unrecognized-shape tail.
- **Rollback / dual-architecture:** `conformist conform --no-edit` (alias
  `--print-only`) reproduces today's exact behavior (write absent files, print
  the full flake snippet, never touch flake.nix). The print-only path remains the
  permanent fallback for unrecognized shapes; `--no-edit` makes it
  user-selectable. Reverting in-place editing is a one-flag change, not a code
  revert.
- **Promotion criterion** for trusting in-place editing as default: clean
  in-place runs across the 13 not-yet-conformist active repos with the edited
  flake still `nix`-parsing.

## Testing

- **`flakeedit` golden tests** (mirror doppelgang's `nixedit_test.go`): fixtures
  per shape — eachDefaultSystem with/without `...`; partially-conformist
  (idempotent no-op); `raw` (igloo-like) and `hybrid` (eng-like) →
  `ErrUnrecognized`; conflicting `formatter`/`devShells`. Assert byte-exact
  output + applied/skipped report + re-apply idempotency.
- **`conform_test.go`**: brownfield (recognized) → edited; already-conformist →
  no-op; greenfield → full write; unparseable → other files still written +
  print-only tail.
- **Parse-safety smoke** (tuning lever): a `just` recipe runs
  `nix-instantiate --parse` over edited fixtures so a malformed splice fails CI.

## Tuning levers

- **devShell list-merge** — splicing our 3 packages into an existing
  `packages = [ … ]` list is doable with the same balanced-run technique but
  deferred; MVP skips + reports it. *Signal to build it:* it's the most common
  real conflict on brownfield repos.
- **formatter replace** — MVP reports rather than overwrites an existing
  `formatter`. *Signal:* could offer `--force-formatter` if repos routinely have
  a placeholder formatter to displace.
- **recognized-shape roster** — currently `eachDefaultSystem` only. *Signal to
  widen* (e.g. add eng's hybrid or a raw shape): a real repo we want to conform
  lands outside the roster.
- **parse-safety smoke in CI** — deferred if it complicates the lint lane.
  *Signal:* a splice bug ships that produces unparseable Nix.
