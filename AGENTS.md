# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) and other coding
agents when working with code in this repository. `CLAUDE.md` is a symlink to
this file.

## What conformist is

conformist is **the linter and formatter multiplexer**: a clean copy of
[treefmt](https://github.com/numtide/treefmt) v2.5.0 (independent project, not a
GitHub fork) that adds first-class _linting_ on top of treefmt's formatter
multiplexing, per `docs/rfcs/0001-linter-support-and-check-repair-modes.md`. It
walks the tree, matches files to tools by glob, and runs matched tools in
parallel, only on files that changed since the last run.

The defining extension over treefmt is the `[linter.<name>]` config section
(parallel to `[formatter.<name>]`), the `conformist check` subcommand, and
**repair** vs **check** modes. A linter's `command` is a read-only check (must
exit non-zero on findings); an optional `repair-command` applies autofixes.

## Where the reference lives

This file is an orientation map, not a reference (conformist#120). The man pages
under `doc/` are normative; read the `.scd` source, since conformist's pages are
not yet installed and so do not resolve via `man` in an agent session.

| Page | Covers |
|---|---|
| `conformist(7)` | concepts; repair/staged/check MODES and their exit codes; sandbox checking; walking and caching; tree root and scope (glob anchoring, `working-dir`, `--formatters` vs linters); config identity |
| `conformist.toml(5)` | every config key, including `repair-command`, the staging tiers and `repair-must-succeed`; GLOB PATTERNS |
| `conformist-nix(7)` | the flake conventions the `flake-*` linters enforce; GO MODULE LOCK; CODEGEN DRIFT; the MODULE LIBRARY a consumer evaluates, the linter registry and the presets |
| `conformist-git(7)` | merge drivers, remotes, default branch |
| `conformist-conform(7)` | how `conform` and `flakeclobber` rewrite a `flake.nix`: the parser, the shape roster, the refusals, splicing, sweep order |
| `conformist-testing(7)` | build backends, test and lint lanes, sandbox constraints, diagnostic recipes |
| `conformist-justfile(7)` | the justfile conventions (whose linters ship from just-us) |
| `docs/features/` | FDRs — one feature's design, limits and measurements |
| `docs/rfcs/` | design records for larger changes |

## Build / test / lint commands

Justfile recipes are **paved paths** — prefer them over ad-hoc
`go build`/`go test`/`nix build`. The default recipe is the local CI lane and is
exactly what `spinclass merge-this-session`'s pre-merge hook runs (`just`), so do
not run `just`/`just lint` again right before merging.

Do NOT enumerate recipes here. Every recipe's name and one-line doc comment is
hoisted into the agent's system prompt by the just-us clown plugin (`just --list`
otherwise), so a listing is duplication that only goes stale — and AGENTS.md is
capped at 40000 characters by the `agents-md` linter. What belongs here is the
rationale a doc comment has no room for.

- `just` (= `just default` = `validate build test verify lint`) is the local CI
  lane and exactly what the merge hook runs, so do not run it again right before
  merging.
- **No ambient Go.** Dependencies live in `go.nix` (igloo FDR 0008): no go.mod,
  go.sum or gomod2nix.toml in the checkout, no `go` in the devShell. go commands
  run inside nix via `godyn-go`; see godyn(7).
- **Everything else about the build and test machinery is in
  `conformist-testing(7)`** (`doc/conformist-testing.7.scd`): the godyn and bga
  backends and the static release binary, the per-package test lane and its
  `tags = ["test"]` instance, the two rules that exist because of bugs (stub
  scripts resolve bash absolutely; `TestMain` pins the ceiling directories,
  conformist#15), the four lint lanes, what the fixture sandbox cannot do and
  which tests therefore live outside CI, and the diagnostic recipes.
- `just bump-version` / `just tag` / `just release` — versioning (release only
  from `master`). Release-on-merge: `just bump-version-level bugfix|minor|major`
  commits the bump before merging; the `release` post-merge target
  (`just deploy-release`) tags the merged commit, pushes `release/vX.Y`, creates
  the forge release (smith) and attaches the tag-built static binary.

**Modes and exit codes are documented in the man pages, not here** (conformist#120).
`conformist(7)` MODES is normative for repair, `--commit`, `--amend`,
`--exit-zero-on-fix` and `--staged` — including partial staging, the
conflict-marker refusal, and `check`'s 0/1/2 — and `conformist.toml(5)` LINTER
SECTIONS for the staging tiers (`restage-repair-outputs`, `stage-new-outputs`,
`stage-deleted-outputs`) and `repair-must-succeed`. Read them as
`doc/conformist.7.scd` and `doc/conformist.toml.5.scd` — the in-repo sources. The
`manpages` derivation builds them, but they do not resolve via `man` in an agent
session and are absent from the hoisted manpage index, so the `.scd` file is the
reachable copy.

## Architecture

### Go program (the engine)

- `main.go` → `cmd.NewRoot(version, commit)`. `version`/`commit` are injected at
  build time by igloo's Go builders — `buildGoApplication` (bga, the default) and
  `buildGoAuto` (the opt-in godyn backend) both emit `-X main.version` from `version.env` and
  `-X main.commit` from `self.rev`; a plain `go build` leaves them `dev`/`unknown`.
  See `eng-versioning(7)`.
- `cmd/` — cobra commands. `root.go` is the entry point: the bare command
  (`conformist <paths...>`, `ArbitraryArgs`) runs format/repair via `format.Run`,
  or `format.RunCommit` with `--commit` (#24: repair, then commit exactly the
  files the run changed — the pre/post `git status` delta — as
  `chore: conformist fmt+fix`; dirty-tree policy in `commitPreflight`);
  subcommands `check` (`check.go`), `identity` (`identity.go` — prints the
  resolved config/toolchain identity hash, conformist#76), `codegen-repair`
  (`codegenrepair.go` + `cmd/codegen/` — #124: applies every
  `checks.<system>.*.passthru.codegenPatch` (igloo#80) with `git apply -p2`, one
  patch per pass since each is a diff against its own check's `src`; config-free so
  the same code ships as the static artifact. Design, limits and measured cost:
  `docs/features/0001-generic-codegen-repair-linter.md`) and `version`
  (`version.go`) dispatch
  separately; `conform` (`conform.go` + `cmd/conform/`) scaffolds a repo into the
  eng shape and edits a recognized `flake.nix` in place. **That machinery is
  documented in `conformist-conform(7)`** (`doc/conformist-conform.7.scd`) — the
  two-pass PEG parser and why `shared.peg` is enforced rather than copied, the
  recognized-shape roster and the deliberate refusals, `//`-merge shadowing,
  splicing and partial-state discrimination, the PAPI template mode, and
  flakeclobber including its sweep order. Read that page before changing anything
  that rewrites a consumer's flake.
  A hidden `gen-man` (`genman.go`) renders the section-1 man pages
  from the cobra tree at build time; `--init` writes a starter config via
  `cmd/init`, `--completion` emits shell completions. Config flags live on
  **persistent** flags so `check` inherits tree-root/walk/excludes/config-file.
- `cmd/flakeclobber/` — a separate binary for fleet migration (RFC 0004,
  conformist#99/#100), sharing the parser from `cmd/conform/flakeparse/`. Its
  behaviour, refusals, exit codes and the sweep-order trap are in
  `conformist-conform(7)`. Do NOT wire it into `conform` — it is an intentionally
  separate one-shot sweep tool. Built by the bga package (`subPackages`) so it
  cannot rot unbuilt again.
- `config/` — viper + TOML config loading. Config discovery searches upward for
  `conformist.toml`/`.conformist.toml`, with `treelint.toml` as a legacy
  fallback from the pre-rename `treelint` name (env: `CONFORMIST_CONFIG`).
- `format/` — the core pipeline. Files are matched to tools by glob, then batched
  by their **formatter sequence** (a `batchKey` like `deadnix:statix:nixfmt`);
  `scheduler.go` runs batches concurrently (errgroup limited to `NumCPU`).
  Per-file **signatures** (md5 of the formatter sequence + file mod-time/size)
  drive change-detection caching so unchanged files are skipped. Whole-tree
  checks (`passes-files=false` linters) are cached separately (conformist#16):
  `check.go`'s `Finalize` runs them once over their full matched set and keys a
  per-check cache entry on the config + an order-independent union of the matched
  files' signatures, skipping the check when nothing it matches has changed.
  `Finalize` runs AFTER all formatter batches, so a whole-tree repair's output
  is NOT reformatted in the same run: it must emit already-formatted text.
  `check.go` / `repair.go` are the two modes; `sandbox.go` implements the
  copy-and-diff strategy that lets fix-only formatters be _checked_ without
  writing to the source tree (so checks work on a read-only tree); `linter.go`,
  `composite.go`, `glob.go` round out matching and linter execution. `exec.go`
  resolves each tool's `command`/`check-command`/`repair-command` into an
  `invocation` — a bare PATH executable run directly, or a shell line run via the
  in-process `mvdan.cc/sh` interpreter (so a command can `cd` into a subdir or
  chain steps) — and every formatter/linter also honors a `working-dir` subdir
  (`workingdir.go`); both are conformist#38.
- `walk/` — pluggable tree walkers: `filesystem.go`, `git.go`, `jujutsu.go`,
  `stdin.go`, selected by `type_enum.go`. `walk/cache/` is the bbolt-backed
  (`go.etcd.io/bbolt`) cache: a `paths` bucket for per-file format signatures,
  a `wholetree` bucket for whole-tree check signatures (conformist#16), and an
  `attestation` bucket holding the tree's config/toolchain identity recorded by
  the last successful repair/format run (conformist#76 — `ReadAttestation`/
  `WriteAttestation`, used by the format path to detect a competing config).
- `stats/`, `git/`, `jujutsu/` — run statistics and VCS helpers (git runs via
  `git.Binary`, a Nix-burned store path).
- `profile/` — RFC 0005 POC v1 resolver behind EXPERIMENTAL `check --profile`
  (rules in its package doc; rule jq is in-process gojq via `rulejq/`; https
  profiles are signature-verified, RFC 0005 §3.2). Under `--profile-only`, an
  unset `--profile`/`--profile-key` falls back to `$CONFORMIST_PROFILE` /
  `$CONFORMIST_PROFILE_KEYS` (sweatfile-provided pins); plain `check` ignores them.
  `just explore-profile-check` runs the gate.
- `test/` — integration harness and fixtures (`test/config`, `test/examples`).
  Fixtures under `test/**` are **deliberately mis-formatted**; they are excluded
  from conformist's own self-lint and must not be reformatted.

### Nix module library (`nix/`)

conformist ships a Nix module like treefmt-nix, extended to cover linters. It is
**self-consumed**: conformist lints/formats its own tree with its own module
(no treefmt-nix dependency — issue #4).

- **The module library is documented in `conformist-nix(7)` MODULE LIBRARY**
  (`doc/conformist-nix.7.scd`): `evalModule`, `mkFormatterModule`/`mkLinterModule`,
  `writeCheckScript`, `wrapWithToolchain`, `mkToolchainHooks`, the freeform
  per-tool submodule, the remarshal-free `mkTomlFormat`/`mkYamlFormat`, and the
  `presets.{eng,eng-go,eng-impure}` rosters.
- `nix/programs/` + `programs.nix` — the formatter registry.
- `nix/linters/` + `linters.nix` — the linter registry; adding
  `nix/linters/<name>.nix` registers a linter. **The roster is in
  `conformist-nix(7)` MODULE LIBRARY** ("Registries"), which names each
  eng-convention enforcer and the page normative for the rule it checks, covers
  `clippy`'s opt-in/impure/no-pinned-Rust design, and explains why the seven
  `justfile-*` linters ship from just-us instead (a conformist→just-us input would
  be a cycle) and why conformist consequently does not run them on itself.
- `nix/presets/` — the `eng` / `eng-go` / `eng-impure` rosters a consumer imports
  instead of enabling each linter by hand; see `conformist-nix(7)` MODULE LIBRARY
  ("Presets") for what each contains and why they are split.
- `nix/conformist.nix` — conformist's own self-config: `imports = [
  ./presets/eng.nix ./presets/eng-go.nix ]` (so conformist dogfoods the canonical
  goimports+gofumpt chain rather than the plain `gofmt` it used to be an outlier
  on) + its own `nixfmt`/`taplo` formatters, `shellcheck`, the Go-specific
  `golangci-dewey`, and excludes (sandboxed, file-based checks).
  `nix/conformist-impure.nix` — `imports = [ ./presets/eng-impure.nix ]`; the
  impure git-state checks need a live `.git` and so run via `just lint-worktree`
  against the working tree rather than the sandboxed `checks.formatting`.
- `nix/checks.nix` — eval-only smoke test forcing module eval + config generation
  for every ported formatter/linter (`checks.<sys>.{formatter-*,linter-*}`).
- `nix/linter-fixtures.nix` — **behavioral** fixture tests for the whole-tree
  linters (conformist#17): `mkLinterFixtureCheck` evals a linter module, pulls
  its `settings.linter.<name>.command`, and runs it against a crafted pass/fail
  fixture tree, asserting the exit code + an output token. Closes the gap where a
  linter's failure path / language variant was only verified by hand (the #29
  Cargo lane, #23 undocumented-debug rejection). Exposed as
  `checks.<sys>.{linter-fixture-<name>-<label>, linter-fixtures}`; the aggregate
  is built cheaply by `just verify-linter-fixtures` (in the `verify` lane, so the
  merge hook gates it — NOT a full `nix flake check`, which would also realize
  the ~130 registry smoke checks).

### Flake outputs (`flake.nix`, `flake-module.nix`)

- Inputs: `igloo` (amarbel-llc/nixpkgs fork: godyn's `buildGoAuto` /
  `buildGodynModule`, `godyn-go` / `godyn-test`, the registry toolchain
  `pkgs.goToolchain.go` (FDR 0012), `nixgc`), `nixpkgs-master` (pinned, the
  devShell's gofumpt), and `utils`. Outputs bind `@inputs` for go.nix
  resolution. **conformist deliberately does NOT take `purse-first` as a flake
  input** — it must stay strictly upstream of purse-first (no cycle). It builds
  the dewey analyzers (defererr, repool, seqerror, testui) itself from a
  **fixed-output source fetch** (`deweySrc`: `fetchFromGitHub` pinned by
  rev + hash; `buildGoApplication` over `libs/dewey` against purse-first's
  workspace gomod2nix.toml, `GOWORK=off`) — an FOD leaf pins source by commit and
  pulls no flake graph; bump rev+hash deliberately. **`nixpkgs-master` is the
  single sha source**: igloo's input follows ours, which only works because
  `pkgs` is `igloo.legacyPackages.<sys>`, NOT the follows-immune
  `import igloo {}` shim (igloo#37).
- `packages.{default,conformist,conformist-bga,conformist-static,manpages}` and
  the `checks.<sys>` lanes — see `conformist-testing(7)` for the backends and
  their trade-offs. `conformist-godyn-tests` is the tests instance for
  `godyn-test -A`; self-consumption evals use the bare binary.
- **Man pages** (`doc/`, `eng-manpages(7)`): hand-written scdoc for sections 2–9
  plus the codegen section-1 reference via `conformist gen-man`, all compiled by
  the `manpages` derivation — that build IS the man-page lint (PRINCIPLE 4), so
  there is no justfile recipe. Exposed as `packages.manpages` and joined into
  `packages.default`, which is how a consumer (circus/eng) installs them. The
  pages are the normative home for conformist's conventions; each names its
  subject in its NAME line. Note `doc/` (man-page sources) is distinct from
  `docs/` (the mkdocs prose site, RFCs and FDRs).
- `formatter` (= `nix fmt` wrapper), `checks.formatting` (sandboxed read-only
  gate) + the `formatter-*`/`linter-*` registry smoke tests.
- `lib` = the Nix module library (`conformist.lib.evalModule pkgs { … }`), which
  also carries `lib.presets.{eng,eng-go,eng-impure}` (the one-import eng rosters,
  see `nix/presets/`) and `lib.profile` (`conformist.profile` as a path — circus
  signs and serves it from its conformist input); `flakeModule` = `flake-module.nix` (flake-parts
  `perSystem.conformist`).
- `templates.eng` (`templates/eng/`) — `nix flake init -t
  'git+https://code.linenisgreat.com/conformist.git#eng'` scaffolds an adopter
  repo wired to the eng
  preset (flake.nix + conformist.nix + a conformist-justfile(7)-conformant
  justfile + version.env + .envrc + a sweatfile wiring the config-specific
  `conformist-pre-commit` hook plus the opt-in `conformist-repair` merge-time
  hook — conformist#47/#51/#54/#59). The flake's `build.preCommit` and
  `build.repair` are exposed both as `packages.conformist-pre-commit` /
  `packages.conformist-repair` and on the devShell PATH, so the sweatfile's
  `pre-commit = "conformist-pre-commit"` resolves to the toolchain-hermetic hook
  rather than a bare `conformist --staged` (which silently skips file types whose
  formatter isn't on PATH); `repair = "conformist-repair"` ships commented (the
  per-commit hook already keeps the tree conformant, and `--amend` re-signs HEAD),
  a documented opt-in with a path to flip the default later.
  `cmd/conform`'s scaffold ships the same flake.nix/justfile/sweatfile
  byte-identically (a drift test guards it). `templates/**` is excluded from
  conformist's own self-lint (consumer-facing scaffold, e.g. a direnv `.envrc`
  has no shebang); the `explore-template-eng` recipe smoke-tests instantiation.
  Downstream consumers MUST set `conformist.package` — conformist is not in
  nixpkgs, so the module's `package` option has no default.

## Conventions and gotchas

- **A check that passes or fails for an incidental reason is worse than no
  check, because it is believed.** Three real instances in this repo, all
  caught only because a result contradicted a direct invocation:
  - A `"$bin" … | grep -q 'not the recognized'` oracle under
    `set -o pipefail` inherits the tool's own exit 1, masking grep's success —
    so it reported "parses" in exactly the failing case. Capture output to a
    variable, then match; never end a pipeline in `grep -q` under `pipefail`
    when the left-hand side exits non-zero by design.
  - Every case in `TestParseFlakeUnrecognizedShapes` once lacked an `inputs`
    block. `ParseFlake` requires one, so each case refused for that trivial
    reason and never exercised the shape it claimed to pin. A refusal-roster
    test needs a **positive control** asserting the wrapper itself parses, or
    the refusals prove nothing.
  - `debug-flakeclobber-regression` first diffed output containing the target
    path, which necessarily differed between the two runs — reporting a
    fleet-wide regression while the bytes were identical. Normalise
    run-specific values (paths, temp dirs, timestamps) before comparing.

  When a verification result is surprising in EITHER direction, re-derive it
  by running the underlying tool by hand before acting on it or reporting it.
- **`nix build` against a dirty tree only sees git-tracked files.** `git add`
  new `.go`/`.nix` files (staging is enough, no commit) before `nix build`, or
  you'll get phantom "cannot find package" errors.
- **Single version source of truth:** `version.env` (`CONFORMIST_VERSION`). Bump
  via `just bump-version`; never hand-edit ldflags. See `eng-versioning(7)`.
- This repo enforces eng-wide conventions on itself. Before adding a justfile
  recipe, changing release tagging, or touching the version/flake wiring, read
  the matching `eng-*(7)` manpage (`eng-design_patterns-justfile(7)`,
  `eng-versioning(7)`, `eng-manpages(7)`) — the linters in `nix/linters/` will
  fail the build otherwise.
- `docs/` (mkdocs site + RFCs + FDRs under `docs/features/`, per the `eng:fdr`
  skill) is prose and is excluded from code formatters; do not expect `nix fmt` to
  touch it. Only `docs/site/` is part of the mkdocs build, so `docs/rfcs/` and
  `docs/features/` need no nav wiring.
