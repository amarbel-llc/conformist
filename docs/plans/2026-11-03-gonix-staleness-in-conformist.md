# Should a "go.nix is stale" check live in conformist?

Date: 2026-11-03. Session: conformist/quiet-hickory (confined researcher).

Labels: **V** = verified by reading the cited file in this worktree. **I** = inferred. **U** = unverified (could not read the source).

## Read this first: what I could not read

- The spinclass report `docs/plans/2026-11-03-gonix-churn-root-cause.md` is in a sibling repo and Read was denied. Nothing below relies on it.
- `godyn(7)`, `mkGoPkgs(7)` and igloo's source are unreadable here (sibling repo, and the man MCP server is stopped). Everything about how godyn resolves `flakeInputs` into a producer's go.mod, and whether igloo already has a staleness check, is **U**.
- The task named `canonical-inputs` and `dead-overrides` as example linters. Neither exists in this repo: `nix/linters/canonical-inputs.nix` and `nix/linters/dead-overrides.nix` both fail to open (**V**). If they live in doppelgang or nixpkgs, that is outside what I could read.

## 1. How conformist runs checks

- **Two kinds of tool (V).** `conformist(7)` FORMATTERS AND LINTERS (doc/conformist.7.scd:20-35): a linter's `command` is read-only and exits non-zero on a finding. An optional `repair-command` applies fixes. Modes are repair (default), staged (git pre-commit) and check (read-only, exit 0/1/2) (doc/conformist.7.scd:37-120).
- **Whole-tree linters (V).** `passes-files = false` runs once at the tree root. `includes` is only a trigger gate. A linter whose matched set is empty is skipped (doc/conformist.7.scd:180-187; docs/features/0001-generic-codegen-repair-linter.md:255-263).
- **The pure lane has no nix and no network (V).** The sandboxed `checks.formatting` and the linter fixtures run in a pure sandbox with "no network, and no nix on PATH" (doc/conformist-testing.7.scd:110-114). A linter in that lane sees only committed files. The impure lane (`eng-impure`, `just lint-worktree`) has a live `.git`; the gomod2nix linter runs there because it needs the module graph (doc/conformist-nix.7.scd:69-79).
- **Profile (V).** `conformist.profile` today carries only linters delivered as pinned static artifacts: the justfile rules via `just`, and `manpage-names` via doppelgang (conformist.profile:27-161). A profile is data plus pinned binaries. It cannot depend on a flake build output; the file says so for manpage NAME lines (conformist.profile:150-155). The `eng` preset is a Nix-module import (nix/presets/eng.nix:47-87).
- **Pattern: "derived state must match its source" (V).**
  - `gomod2nix` regenerates fresh into a temp dir and diffs against the committed file. Its repair regenerates in place and stages it. It runs in the impure lane because it needs the real module graph (nix/linters/gomod2nix.nix:81-123, 125-151).
  - `codegen-repair` is the pattern for "the flake already has a drift check". It is repair-only: its `command` is a deliberate no-op, because the flake check carrying `passthru.codegenPatch` is the check-mode gate (nix/linters/codegen-repair.nix:11-16, 51-62). It builds every such patch and applies it with `git apply -p2` (doc/conformist-nix.7.scd:84-147).
  - `mkLayoutCheck` and `mkManpageNamesCheck` are flake-check helpers in conformist's lib for rules that need a build output. The layout one says outright that "a conformist linter over the raw tree could not run it" because a go.nix repo has no go.mod in its tree (doc/conformist-nix.7.scd:192-206, 207-221).

## 2. Could a conformist linter detect go.nix staleness offline?

- **What go.nix holds (V for the file, U for the semantics).** conformist's own go.nix has `flakeInputs = { }` and a `require` table of `{ version, hash, go?, indirect? }` entries (go.nix:4-60). The template comment says fleet modules go in `flakeInputs` ("an input of flake.nix, resolved as its go-pkgs") and third-party modules go in `require` (templates/eng-go/go.nix:1-5). That fits the premise that producers' third-party requirements are pinned in `require`. The resolution mechanism is **U**.
- **A raw-tree conformist linter cannot do it (I, strongly).** The producer's go.mod is not in the consumer's tree. `flake.lock` carries only rev and narHash. Reaching the go.mod means realizing the input in the store, i.e. nix. The pure lane has no nix (see above), and the codegen lane's cost with nix is about 1.5-2.2s per discovery with no eval cache (docs/features/0001-generic-codegen-repair-linter.md:220-241).
- **An impure-lane linter could do it (I).** It would run `nix eval` or `nix build` against the flake and compare. That is the codegen-repair shape, and it is heavy for a read-only check. The offline claim is **U**: evaluating a consumer's flake may need to fetch inputs it has not yet realized. Right after `nix flake update` they are realized, but a fresh clone may not have them.
- **Likely false positives (all I).**
  - A deliberately higher pin in go.nix than any producer's minimum. The check must be "go.nix >= max required", not equality, or it flags legitimate bumps.
  - Entries left behind after a producer dropped a requirement ("extra" versus "missing" need separate rules).
  - `indirect` and `go` fields, and `replace` entries that shadow a producer's requirement.
  - Compare versions only: hashes cannot be checked offline without trusting the stored one.
- **Duplicate or complement an igloo check?** Whether igloo has such a check today is **U** (igloo#98 proposes one). The logic belongs next to the code that defines go.nix semantics: godyn already derives the package graph from go.nix at eval time (doc/conformist-testing.7.scd:35-36), and igloo owns that schema. A conformist copy of the version-comparison rule would be a second implementation of igloo's schema and would drift with it. The same split is already in use: conformist's `mkLayoutCheck` is a thin wrapper over `app.passthru.codegenCheck`, which godyn provides (doc/conformist-nix.7.scd:196-203).

## 3. Fix versus check

- **The repair hook already exists and already fires on the right files (V).** `codegen-repair` is enabled by `presets.eng` (nix/presets/eng.nix:64) and its default trigger globs are `*.go`, `flake.lock` and `go.nix` (nix/linters/codegen-repair.nix:122-128). A commit that stages `flake.lock` therefore already triggers it. If igloo's go.nix freshness check carried `passthru.codegenPatch` (igloo#80 contract), the conformist lane would build the patch and apply it with no conformist change. Whether igloo's check can produce such a patch is **U**. The hard part is that go.nix entries carry hashes of new versions, so the patch derivation would need network or fixed-output knowledge.
- **A direct repair-command is poor (I).** Running `godyn-go -- go mod tidy` from a conformist `repair-command` needs `impure-derivations` and the network (doc/conformist-testing.7.scd:24-26). In a pre-commit hook that blocks on non-zero exit, the codegen lane deliberately fails soft for exactly this reason (docs/features/0001-generic-codegen-repair-linter.md:62-70). `igloo#99` (no patch-only mode) means that the tidy would also rewrite unrelated entries, which is the churn being reported. A repair hook would hide that churn inside commits.
- **Fail-soft trade-off (V).** Under the codegen lane a nix failure only warns. So a conformist repair can never be the enforcement: only a check can (docs/features/0001-generic-codegen-repair-linter.md:144-157, 271).

## 4. Recommendation

**Primary: an igloo flake check (owner: igloo), exposed so conformist's existing lanes pick it up. Not a new conformist linter.**

1. **igloo** ships the freshness check on the godyn module (igloo#98), comparing go.nix to the max of the pinned producers' requirements, with "go.nix >= max" semantics. It is the only place that can read producers' go.mod offline from the store and the only owner of the go.nix schema.
2. **Where the repair is possible**, igloo attaches `passthru.codegenPatch` (igloo#80). conformist's `codegen-repair` lane then repairs at commit time whenever `flake.lock` or `go.nix` is staged, for every repo on `presets.eng`. Zero conformist code. This is the cheapest route to "enforced in every repo that has a go.nix", but only for repos on `presets.eng`.
3. **The merge gate** is the check itself via `checks.<system>`, which the default `just` recipe builds. That gives enforcement in any repo with a go.nix, whether or not it uses conformist.
4. **The cascade step** (`godyn-go -- go mod tidy`, quiet-maple) is the proactive half. It is the only thing that runs immediately after `nix flake update`. Owner: the circus cascade, plus each repo's `update-*` recipe.
5. **conformist's role (small):** if igloo prefers, conformist adds a thin `mkGoNixFreshCheck` helper in `nix/` modelled on `mkLayoutCheck`, wired into `templates/eng-go` as `checks.go-nix-fresh`. That documents it in `conformist-nix(7)` and gets it into every new Go repo from the template. Only worth doing after igloo's check exists.

**Smallest first step:** igloo implements the offline check from igloo#98 and decides whether it can emit a `codegenPatch`. Until then conformist can do one cheap thing: extend the `codegen-repair` docs to name go.nix as a supported trigger target. No code.

**Enforcement ordering after `nix flake update` (I; no tool fires on the update itself):**

1. Update step: the cascade or the repo's update recipe runs the godyn tidy immediately (the only point that is truly "right after").
2. Next commit: pre-commit `codegen-repair`, if `flake.lock` or `go.nix` is staged and the igloo check carries a patch.
3. Merge gate: the flake check fails the pre-merge `just`.

Gap: conformist cannot fire on `nix flake update` itself, only on the next commit or merge that touches the trigger files. A flake.lock change committed with `--no-verify`, or made by a path that skips the hook, is caught only by step 3.

## Issues I would file (not filed)

- **igloo:** the freshness check proposed in igloo#98, with an explicit "go.nix >= max of producers" rule and an "extra entries" rule. Plus a decision on whether it carries `passthru.codegenPatch`.
- **conformist (after igloo lands):** wire the check into `templates/eng-go` and document it in `conformist-nix(7)`. Optional `mkGoNixFreshCheck` helper.
- **conformist:** doc note that `codegen-repair`'s `includes` already cover `go.nix` and `flake.lock`, so a go.nix patch needs no config.
- **circus/quiet-maple:** nothing new; confirm the cascade step runs after the flake update and before the commit, and that it keeps `go.nix` free of unrelated bumps given igloo#99.

## Open questions for the operator

1. Please send the pure-birch report text, or have igloo's session confirm the go.nix `flakeInputs` resolution (U above).
2. Is "enforced in every repo with a go.nix" meant to include repos that do not use conformist at all? If yes, only the igloo check covers them.
