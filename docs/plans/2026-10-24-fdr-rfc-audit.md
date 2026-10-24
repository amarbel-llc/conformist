# FDR/RFC audit, 2026-10-24

Scope: `docs/features/0001-*` (the only FDR) and `docs/rfcs/0001`–`0005`. Per
document: is the status true, do the claims match code and manpages, are links
and issue references live, is anything shipped but undocumented or documented
but unbuilt.

Evidence labels: **observed** = I ran a search, listed a directory or fetched an
issue/file this session and saw the result. **read** = I read the document or a
comment and did not check it against the thing it describes.

## Fixed in this change (plain drift)

| Doc | Was | Now | Evidence |
|---|---|---|---|
| RFC 0001 §4 | "Whole-tree checks are not yet incrementally cached" | cached since #16 | observed: `format/check.go` `Finalize` and `cmd/cache_wholetree_test.go` (`TestCheckWholeTreeCache*`); #16 closed |
| RFC 0001 Compatibility | config names "treefmt.toml", "until that rename lands" | rename landed; names and `CONFORMIST_CONFIG` stated | observed: `cmd/root.go` `loadConfig` filenames list (`conformist.toml` first, `treelint.toml` legacy) |
| RFC 0001 Conformance Testing | points at `zz-tests_bats/` | says the directory does not exist; tests are Go | observed: no `zz-tests_bats` entry in the repo root listing; a repo-wide search for the name matches only RFC text, `doc/conformist-justfile.7.scd` and one flakeedit testdata file; no `test_check*.bats` anywhere |
| RFC 0003 §7 | purse-first "not yet wired", "has no conformist.toml" | dated note: it now publishes `lib.conformistLinters`/`lib.conformistModules` | observed: purse-first `flake.nix` fetched from the forge shows both outputs; `conformist.toml` is still absent there (404) |
| RFC 0004 §5 | "`verify-flakeclobber-parse` is not yet implemented, tracked as #99" | exists; #99 closed | observed: recipe in `just --list`; #99 state Closed |
| RFC 0004 §4 | flakeedit "refuses 3-of-4 repos entirely", gap #100 | past tense; #100 closed; points at `conformist-conform(7)` | observed: #100 state Closed. NOT observed: the mechanism that closed it; the new text defers to the manpage rather than asserting one |

## Findings that need a decision (not changed)

1. **Status vocabulary is unclear and the five RFC statuses look stale.**
   The RFCs use `proposed`/`exploring`; the FDR uses `experimental`. The system
   index lists the same values. I could not find a written definition of the set
   (the `eng:rfc` skill text was not read this session), so I did not change any
   status.
   - RFC 0001 (`proposed`): observed shipped. `[linter.<name>]`, `check`, sandbox
     checking and exit codes are all in `config/`, `cmd/check.go`, `format/`.
     Candidate for "accepted"/"implemented" if the vocabulary has one.
   - RFC 0004 (`proposed`): observed shipped. `cmd/flakeclobber` has the
     `--old/--new/--apply` flags, `ErrNoDevShell`, `ErrPartialState` and the
     `nix-instantiate --parse` verify step.
   - RFC 0005 (`proposed`): partly built. See finding 3.
   - RFC 0002 and RFC 0003 (`exploring`): §2–§3 of 0002 and §2–§4 of 0003 are the
     parts the documents call actionable, and observed shipped (see below), while
     §4 of 0002 and §5–§6 of 0003 are explicitly open. One status per document
     cannot say that; the operator should decide whether to split or promote.
2. **Issues #44 and #45 are open on the forge but their fix has shipped.**
   RFC 0001 says the `ignore-global-excludes` flag was removed in #45;
   `doc/conformist.toml.5.scd:192` and `format/check.go:82` say it was retired,
   and `grep` finds no `IgnoreGlobalExcludes` field in the code. RFC 0003 calls
   them "open", which is literally true of the tracker and false of the code.
   Observed: both fetched, state Open. I did not close them (closing is the
   operator's call; #45 has a comment I did not read).
3. **RFC 0005's v1 scope no longer matches the code.** §7 lists signature
   verification as out of scope for v1. Observed: `profile/signature.go`,
   `signature_internal_test.go` (rotation, published keys, fetch of a signed
   profile) and `signature_vector_internal_test.go` (papi §15 vector) exist, as do
   per-system artifacts (`system.go`, `system_test.go`) and preludes
   (`resolve.go`, `document.go`). Still absent, observed in `document.go`: layer
   walking and delegation (`ErrUnsupportedInPOC`). So the POC is between v1 and
   v2. The §7.1 gate ("MUST NOT advance beyond `experimental` until v2
   succeeds") is the status-relevant statement; nothing I saw shows v2 succeeding.
   Not changed, because rewriting the POC scope rewrites a design record.
4. **RFC 0005 Conformance Testing names bats files that do not exist**
   (`profile_parse.bats`, `profile_artifact.bats`, `profile_signature.bats`, and
   so on, under `zz-tests_bats/`). Observed: none exist; the covering tests are Go
   tests in `profile/` (`TestParseRejections`, `TestMarklRejections`,
   `TestResolveRejectsMismatchWithoutCaching`, `TestResolveChecksPinsBeforeFetching`,
   `TestVerifySignature`, `TestPAPISignatureVector`, `TestResolveRejectsAnUnsupportedSystem`)
   and `cmd/profile_test.go` (`TestCheckProfile`). The §3 layer-walk, §3.1
   delegation and §4.3 stanza-source rows have no test because (observed) layer
   walking and delegation are unimplemented; I did not check whether §4.3's
   diagnostic exists. I did not remap the table: each row needs its test read,
   which this audit did not do. Same fix as RFC 0001 is available (note that the
   file column is intent) if wanted.
5. **RFC 0003 "to add" tests.** Three rows are marked "(to add)". Observed
   `TestStagedPartial` (`cmd/commit_test.go:597`) exists and may cover the §6
   carve-out; not read. No test for `--commit <explicit path>` formatting a repair
   output was found by name, which matches RFC 0003 §5 (universal observation is
   still opt-in scoped: `cmd/format/format.go:242-263` folds only the observer's
   output, observed).
6. **RFC 0002 §4 / #58 is still open and the RFC omits `mkToolchainHooks`.**
   Observed: #58 Open; `nix/mk-toolchain-hooks.nix` exists (header: the
   TOML-consumer mirror of `build.{wrapper,preCommit,repair}`, #59). RFC 0002's
   Compatibility section lists only `lib.wrapWithToolchain` as the non-module
   path. Small documentation gap; also `mkLayoutCheck` and the other lib helpers
   are not RFC matters. Left for the operator because it is part of the open
   module-shape question.
7. **RFC 0004 has session residue.** The "Premise Check" section records a task
   brief ("this worktree has uncommitted flakeedit changes...") and the false
   premise about FDR/ADR directories. It is not a claim about the code and is
   no longer useful to a reader. Also `cmd/conform/flakeedit/flakeedit.go:126` is
   cited by line number (read; not checked, line references rot). Both are
   cosmetic; I left them because deleting a section of a design record is an
   editorial call.

## Documents with no findings

- **FDR 0001 (codegen-repair).** Observed: every identifier it names exists
  (`linters.codegen-repair` in `nix/linters/codegen-repair.nix`, enabled in
  `nix/presets/eng.nix:64-65`; `runSplit` in `format/exec.go`; `RepairLinter`
  returning early on empty `files`, `format/repair.go:86-87`; pass limit
  `2*checks + 2` at `cmd/codegen/codegen.go:506`; error tail `maxMessage = 2000`
  at line 1024; the no-worktree soft failure `errNoWorktree` at line 394).
  Issue references #124, #127, #132, #55/#56/#57 all resolve (listed). Observed
  closed: #124, #127, #55, #56, #57. #132 appeared in a search but I did not see
  its state. Not verified: the igloo#80/#81/#83 and tommy#144/spinclass#322
  references (other repos), the cost table (measurements, not rerun), and the
  `max(4, ...)` floor (only the `2*checks + 2` arm was seen). Status
  `experimental` and its promotion criteria cannot be checked from this repo:
  they depend on fleet usage.
- **RFC 0002 §2–§3 (stage-mutation tiers).** Observed: all six tests the
  Conformance Testing table names exist in `cmd/staged_repair_test.go`, plus a
  seventh (`TestStagedFormatsRepairOutputsBeforeRestaging`, which RFC 0003 cites).
  Config keys exist in `config/config.go` (`RepairMustSucceed` and the staging
  fields). Not read: the staging logic itself.

## Not checked

- Cross-repo references: eng FDR 0015, just-us FDR 0003, papi RFC-0001, piggy RFC
  0011, madder RFC 0002 stub, eng#205, purse-first#166, igloo issues. These live
  in other repos' sessions.
- The cost and timing numbers in FDR 0001.
- Whether RFC 0001's exit-code rows match `cmd/check.go` beyond the code comment
  at lines 42-43 (read); `check` has no `--linters` flag (observed: `linters`
  is absent from `cmd/check.go` flags), which RFC 0001 allows as a MAY.
