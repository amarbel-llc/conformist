# check: verify a formatter priority chain as a chain

- **Status:** proposed (design note only; nothing implemented)
- **Date:** 2026-10-23
- **Issue:** [#123](https://code.linenisgreat.com/linenisgreat/conformist/issues/123)
- **Related:** RFC 0001 §3 and §6 (check by sandbox-and-diff), #28 (config files in the sandbox)

## Problem

Repair applies a file's formatters as a **chain**: `scheduler.go` batches files by the
ordered sequence of formatters that match them (`batchKey`, e.g. `goimports:gofumpt`,
sorted by priority in `composite.go`) and runs them one after another.

Check does not. `CompositeChecker.Check` (`format/check.go`) groups files by formatter
and calls `Formatter.check` for each one **independently**, and `checkSandbox`
(`format/sandbox.go`) copies the file as it is on disk, runs that one formatter, and
diffs. So check asks "is the file a fixed point of `goimports`?" and separately "of
`gofumpt`?", while repair produces `gofumpt(goimports(x))`.

Where two chained formatters disagree about a construct, the chain's own output is not a
fixed point of its first member. That is the reported case: gofumpt 0.12 dedents composite
literals in a multi-value `return`; goimports leaves the old shape. `nix fmt` writes the
gofumpt shape, then `check` reports `would reformat: … (goimports)`. **No tree state
passes check**, and repair cannot fix it, so the gate is unfixable by the tool that gates.

This was read from the code, not reproduced here; the reporter's repro (trapeze, igloo
gofumpt 0.12) is the evidence. The golangci-lint-embedded-gofumpt mismatch the issue also
mentions is a toolchain-coherence problem between igloo and nixpkgs-master, and is not
addressed here.

## Options

### A. Chain-aware check (recommended)

Check each file against what repair would produce. Group files by the same ordered
formatter sequence the scheduler uses. For each group make one sandbox, copy the files
and the **union** of the chain's declared `config-files`, run each formatter's repair
command in priority order inside it, then diff the final contents against the originals.
A file whose chain output differs is a finding.

- Repair output always passes check, by construction: check computes the same function.
- One sandbox copy per chain instead of one per formatter, so a multi-formatter chain
  gets cheaper, not dearer.
- A single-formatter "chain" takes today's path unchanged, native check-command included,
  so most output and behavior does not move.

### B. Detect the disagreement, report a config error

Keep independent checks, but when a formatter flags a file that the chain's output
satisfies (or the chain is not idempotent), report "goimports and gofumpt disagree on
<file>" instead of drift.

This needs the same chain run as A to know the chain's output, plus a second pass to test
idempotence, and then still fails the gate: the tree was repaired correctly and is
rejected anyway, with a better message. It diagnoses the problem without removing it.

### Recommendation: A, with B's idempotence test as a guard

Implement A. If a chain's output is itself changed by running the chain again (a chain
that does not converge, so repair and check could still never agree), report that as the
distinct config error B describes. That is one extra pass, run only for files already
found to differ.

## Formatters with a native `check-command`

A native check (`gofmt -l`, `rustfmt --check`) is read-only and tells you whether a tool
would change a file, but it cannot be chained: it does not produce the output the next
formatter needs. Policy:

- A formatter alone in its chain keeps its native check. Unchanged.
- A formatter in a chain of two or more is run through its **repair** `command` in the
  chain sandbox, as `sandbox = true` formatters already are. Its native check is not
  consulted for that file.

Cost: for chained files the native check's speed and its own config discovery are
replaced by the sandbox path, so the member must declare its `config-files` (rustfmt and
gofumpt already do; this is the existing #28 rule, now load-bearing for chains). A
formatter with a `check-command` but whose `command` is not a faithful in-place repair
would misbehave; none in the registry is like that, but the module option docs should say
so.

## Test plan

Hermetic Go tests in `format/` with stub formatters (scripts resolving bash by absolute
path, per conformist-testing(7)), plus the real pair as a nix linter fixture.

1. **The disagreement.** Early stub `E` rewrites shape `S_new` to `S_old` (idempotent on
   `S_old`); late stub `L` rewrites `S_old` to `S_new` (idempotent on `S_new`). Chain
   priority `E` then `L`.
   - File in `S_new`: today fails check forever (E would change it); with A passes.
   - File in `S_old`: finding, and after repair it passes. This is the property: for every
     input, `check(repair(x))` passes.
2. **Positive control** that the harness can fail: a chain whose output differs from the
   input produces a finding naming the chain; a singleton formatter still reports under its
   own name.
3. **Non-convergence.** Two stubs that each undo the other produce the distinct config
   error, not a plain finding.
4. **Config files.** A chain member's `config-files` are present in the one sandbox
   (extends the #28 test).
5. **Native check-command.** A chained member with a `check-command` that would pass is
   still judged by the chain; an unchained one keeps the native path.
6. **Real pair.** A nix fixture with pinned goimports and gofumpt on the issue's
   `return Model{…}, Model{…}, nil` literal. It is only as stable as the gofumpt pin, so
   keep it as a single named fixture that can be updated deliberately.

## Risk to existing output

- **Attribution changes for chains.** Today a finding reads `would reformat: f.go
  (gofumpt)`. For a chain it would name the chain (`goimports:gofumpt`) or the first
  member whose step changed the file. Anything that parses that line, for chains only,
  changes. Singleton formatters, the vast majority, are unchanged.
- **`--formatters` filtering.** Selecting a subset of formatters yields chains of the
  selected ones only, as repair already does with them; check should do the same, not
  verify a chain the caller excluded.
- **Different `working-dir` inside a chain.** Each member runs in its own resolved subdir
  of the one sandbox root. Needs a test; the sandbox already supports per-formatter
  working dirs one at a time.
- **Performance.** Fewer file copies for chained files; no new work for singletons.
- **Cache.** Per-file formatting is not cached in check mode, so no cache key changes.

## Size

Roughly: group-by-sequence in `format/check.go` (reusing the scheduler's sequencing),
generalize `checkSandbox` to run an ordered list of formatters in one sandbox
(`format/sandbox.go`), the non-convergence guard, tests and one nix fixture, and doc
updates to conformist(7) and RFC 0001 §3/§6. Order of 150-250 lines of Go plus tests; a
day's work, mostly in tests. Medium risk, concentrated in attribution and `working-dir`.

## Open questions for the owner

1. Finding attribution for a chain: the chain name, or the first member that changed it?
2. Is the non-convergence guard wanted in the first cut, or a follow-up?
3. Should the module warn when a chained member declares a `check-command` (which the
   chain will ignore), or stay silent?
