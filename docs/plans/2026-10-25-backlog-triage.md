# Backlog triage, 2026-10-25

Scope: the 51 open conformist issues as listed by the forge at the start of this
pass (issue bodies and comments read in full by a read-only agent, digest kept
outside the repo; every verdict below that says V was re-checked by me).

Labels: **V** = observed (I ran it or read the exact lines this session). **R** =
read in an issue body or comment, not checked against the tree. **I** = inferred.

Classes: **(a)** fixable tonight, failing test first, no decision. **(b)** needs an
operator decision (the decision and options are named). **(c)** blocked on another
repo or session (named). **(d)** stale or already fixed.

Held by the coordinator and not triaged for work: #107, #123, the cache class
(#94, #110, #125, #134), #71/#72, #58/#66. They are classified below so the sheet is
complete, but nothing here starts them.

## (a) Fixable tonight

| # | Item | Why (a) | Order |
|---|---|---|---|
| 108 | assert `outputs.peg` `AtName` and `shared.peg` `Identifier` char classes stay identical | the issue says the two rules agree today; the fix is a guard test in `cmd/conform/flakeparse` (V: both rules read at `outputs.peg:72` and `shared.peg:67`, identical text). **Done in the merge that carries this note**: `assertMirroredRules` runs in `compileMatcher`; the package's tests pass, including a positive control and a drift test | 1 |
| 50 | `eng-versioning-deprecated-file`: sanctioned secondary version lineages | V: the linter has no allow/ignore option (searched for `allow`, `ignore`, `sanction`: none). The issue proposes `linters.eng-versioning-deprecated-file.allow`; adding a path-glob allowlist is additive and default-off | 2 |

Not claimed as (a), though they look small: #77 (security policy, see (b)) and
#65's igloo-raw half (a new recognized flake shape: large, with golden fixtures).

## (b) Needs an operator decision

| # | Decision | Options |
|---|---|---|
| 107 | repo description wording ("treefmt fork" vs clean copy) | edit the forge description; also README/docs "fork" wording. Outward-facing, held. |
| 123 | held: chain-aware check (design note merged, fc7ce95) | implement chain-aware check; or declare it toolchain-coherence, not conformist's |
| 71, 72 | RFC 0003 §5 Option A and §6 carve-out A/B/C | the issues recommend A; ratification is the operator's. #73's other two rows wait on these |
| 58, 66 | module shape, RFC 0002 §4 | collapse wrappers into one hook factory; `[hook]` membership; or leave |
| 144 | migrator for existing repos' `lint-worktree` and flake export | write a migrator in `conform`; or document the manual two-line fix (already in conformist-conform(7)); or sweep fleet repos by hand. R: the issue calls the migrator an open design question |
| 116 | signing-key discovery via `/.well-known/papi` | touches the profile/signing path, outside the overnight rule; "not urgent" per the issue (R) |
| 77, 78 | redirect hardening, auth-gated templates | security-policy changes, outside the overnight rule. #78 also needs papi §5 (c) |
| 79, 53 | gomod2nix subdir: `dir` (one) vs `roots` (list) | the two overlap (R). Recommend `roots`, a superset; pick one API before building |
| 64 | `conform` emits crap | needs a cycle check with purse-first before taking `crap` as a dependency (R), possibly an FDR |
| 80 | conformist owns cwd/tree scoping natively | behaviour change that lets eng drop a wrapper; design ask |
| 109 | bump/release operations as an interface | the issue itself says an RFC comes first (R) |
| 86 | resolve flake-input-bridged Go modules from store paths | the `offline` half landed (R, 6b462af); the remainder needs a design for cold-cache resolution |
| 118 | consumer rollout past the go.nix cutover | bump ~33 flakes deliberately; per-repo igloo rev and ca-derivations (R). Fleet-wide, not one repo |
| 13, 27, 37, 36, 48, 5 | new linters and formatters (`--deep`, Markdown formatter, GFM linter, version burn-in, build-verify ports) | each needs a tool choice or a policy (false-positive tolerance, which formatter). #5 is ~120 long builds of low value |
| 12, 46 | eng-manpages coverage | V: no `eng-manpages` module in `nix/` and no match in `presets/eng.nix` (the name appears only in `eng-versioning-deprecated-file.nix` and `scdoc.nix`), so the rules these issues extend are not in this tree. I: probably moved with the eng freeze, or superseded by the scdoc linter plus the templates' `doppelgang lint-man`. Decide: close both, or restate against what exists |
| 17 | umbrella tracker | keep as the roadmap, or close in favour of the individual issues |
| 65 | flakeedit: igloo-raw shape | the eng-hybrid half landed (R, 372ac68); the rest is 5 of 36 refusals, one of them (smith) deliberate (R). Decide whether to widen the grammar or leave refusals |
| 91 | check/repair skew on gomod2nix goVersion attrs | I: likely fixed by #84's shared regeneration recipe (V: `regenSnippet` is shared by check and repair, `gomod2nix.nix:78,136`), but the issue's symptom (goVersion attrs) was not reproduced. Needs a reproduction from the reporter's repo before closing |

## (c) Blocked on another repo or session

| # | Blocked on |
|---|---|
| 114 | doppelgang releases: no static binary to pin (R, comment) |
| 119 | igloo#77: aarch64 cross link (R) |
| 121 | spinclass: how a removed post-merge target resolves (R). Not answerable from this repo |
| 122 | circus / eng: where the release-on-merge text lands; eng is frozen (R) |
| 113 | moxy: the `man` moxin's `man_section` (V: the report concerns tools in the moxy MCP, not a conformist file) |
| 96 | just-us: the `justfile-*` linters moved there (R; V: no `justfile-debug-recipes` in `nix/linters/`) |
| 90 | the dewey-facade-export module: V: no `dagnabitPackage` or `dagnabitCommand` in `nix/`. I: purse-first or dewey owns it |
| 30 | just-us: static `justfile` checks live there now (I: same reason as #96) |
| 135 | purse-first#207: operator directed the render check's home there (R, comment) |
| 120 | circus owns the skill (V: the agents-md linter's error text names `circus:agents-md-condense`, seen in the fixture output). Likely closable, see (d) |

## (d) Stale or already fixed

| # | Verdict | Evidence |
|---|---|---|
| 126 | fixed | V: `just explore-template-eng` at c12a7f5 prints "template instantiates and passes checks.formatting". The template ships `.gitattributes`. The issue's own comment says the same (R) |
| 93 | fixed | V: `gomod2nix.nix:62` `hasGomod2nix = pkgs ? gomod2nix` gates check and repair at eval time; `linter-fixtures.nix:1612` is a fixture that evaluates the module with `gomod2nix` removed from `pkgs`; the full `verify-linter-fixtures` lane passed at c12a7f5 |
| 95 | fixed | V: `agents-md.nix` walks git-tracked files only and has an `exclude-paths` option (lines 29-30, 223); fixtures `walk-gitignored-nested-pass`, `walk-tracked-excluded-pass` pass in the same lane |
| 34 | stale | V: no `GOLANGCI_LINT_CACHE` anywhere in the tree, and `lint-go` is now "vet and lint ... through godyn's per-package lanes" per its recipe doc. The shared-cache cause no longer applies. I: not re-run |
| 131 | half done, rest is #144 | R: #144's body says the generator half landed in 598956c; the existing-repo migration is #144. Close #131 as superseded by #144 if the operator agrees |
| 115 | fixed, left open for its owner | R: comment cites 598956c; V (earlier this session): the fixtures `ssh-with-insteadof-rewrite-pass` and `https-with-insteadof-to-ssh-still-fails` exist and pass. Not tested in a live spinclass worktree |
| 120 | fixed outside this repo | see (c): the skill exists in circus and the linter points at it (V: fixture output) |
| 97, 62 | closed this pass | merged baa6322 and c12a7f5 |
| 73 | partly done | row 2 landed (76cbe42, V: test passes alone). Rows 1 and 3 wait on #71/#72 |

## Real-repo proof: the eng-go dagnabit lane and manpages on a copy of tap

Method (V): `tap` copied into a scratch directory under this worktree's `.tmp/`
(a plain copy including `.git`; the real checkout was only read). The built
`conformist conform` (this tree, c12a7f5) was run against the copy with `-C`, then
the lane's pieces were run by hand. Nothing in the real tap checkout changed.

What happened:

1. `conform` on tap wrote one file, `conformist.nix`. It kept `flake.nix`
   ("unrecognized shape"), `justfile`, `version.env`, `sweatfile` and
   `.gitattributes`, printed the flake wiring to add by hand, and exited with the
   "scaffolded conformist files" status (exit 3, by design).
2. It offered nothing about Go. The printed justfile snippet has `lint-fmt` and
   `lint-worktree` only: no `lint-layout`, no `lint-manpage-names`. This is the
   documented limit in conformist-conform(7): `conform` does not yet detect a Go
   repository, so the layout lane and the manpage lane exist only in the greenfield
   `eng-go` template. Seeing it on a real Go repo confirms the gap is real, not
   theoretical.
3. dagnabit on tap's real tree (inside tap's own devShell, `cd go &&
   dagnabit -n internal`): one finding, `internal/bravo/ndjson` would move to
   `internal/alfa/ndjson`. So tap's layout is not clean today. tap's levels
   (`0`, `alfa`, `bravo`, `charlie`, `delta`) already follow the convention the lane
   enforces.
4. `conformist.lib.mkLayoutCheck` cannot be applied to tap as it stands. It calls
   `app.passthru.codegenCheck`, which only a godyn (`buildGoAuto`) build provides.
   tap builds with gomod2nix's `buildGoApplication`; `nix eval` of
   `packages.x86_64-linux.tap-dancer-go.passthru` returns
   `["go","goCacheEnv","hooks","vendorEnv"]`: no `codegenCheck`. So adopting the lane
   on a gomod2nix repo means migrating it to `go.nix`/godyn first.
5. Manpage lane: tap's `tap-dancer-doc` package builds; `doppelgang lint-man
   --format text --max 72` over its `share/man` printed `1 page(s) checked, 0
   error(s), 0 warning(s)`.

Friction, in order of cost:

- **The layout lane is greenfield-only and godyn-only.** A gomod2nix Go repo gets
  no path to it from `conform`. Options (operator's call): a `bga` variant of the
  helper that runs `dagnabit -n` in a plain derivation over the source; or teach
  `conform` to detect Go and print the lane. Neither is built.
- **The lane would fail tap's first run** (item 3), which is the intended behaviour
  of a layout check, but the fix is "move the package by hand and fix imports": no
  autofix, because the rendered tree is not a git checkout (stated in
  `mk-layout-check.nix`).
- The manpage lane worked unchanged on an existing page.
- `just lint-layout` itself was not run on tap: tap has no such recipe and `conform`
  did not add one. The check it wraps was run through its parts (items 3 and 4).
  The template's own full gate (including `lint-layout`) passed earlier in
  `explore-template-eng-go`.
