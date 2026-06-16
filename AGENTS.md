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

## Build / test / lint commands

Justfile recipes are **paved paths** — prefer them over ad-hoc
`go build`/`go test`/`nix build`. The default recipe is the local CI lane and is
exactly what `spinclass merge-this-session`'s pre-merge hook runs (`just`), so do
not run `just`/`just lint` again right before merging.

- `just` (= `just default` = `build verify lint`) — full local CI lane.
- `just build` — `build-gomod2nix` + `build-godyn-graph` + `build-go` +
  `build-nix`.
- `just build-go` — fast out-of-nix `go build -o build/conformist .` (version
  stays `dev`/`unknown`; only the nix build injects real version/commit).
- `just test` / `just test-go` — `nix develop --command go test -tags test
  ./...` (the `test` tag gates dewey's `test_ui` package, which the test
  helpers use — also set as `run.build-tags` in `.golangci.yaml`). Run a
  single test with
  `nix develop --command go test -tags test ./format -run TestName`. The
  `cmd` integration tests run conformist against `$TMPDIR` fixtures; a `cmd`
  `TestMain` sets `GIT_CEILING_DIRECTORIES` (git tree-root search) and
  `CONFORMIST_CEILING_DIRECTORIES` (config discovery) to the temp root so they
  can't escape into the worktree/monorepo (conformist#15), and `just test-go`
  fails if the working tree is mutated during the run.
- `just lint` — `lint-fmt` (sandboxed `checks.formatting`, file-based linters) +
  `lint-worktree` (impure git-state linters against the working tree, where
  `.git` is available) + `lint-go` (golangci-lint via the purse-first custom
  build carrying the dewey analyzers; `.golangci.yaml` is `default: all` minus a
  curated disable list, plus the `dewey` custom linter — conformist#10/#22).
- `just codemod-fmt` — `nix fmt` (write/repair mode on conformist's own tree).
- `just build-gomod2nix` — regenerate `gomod2nix.toml`; run after changing deps.
- `just update-go` — `go mod tidy` then regenerate gomod2nix.
- `just explore-show-config` — emit conformist's own generated `conformist.toml`
  from the Nix module without a full check run (debugging the module).
- `just debug-bench-backends [iterations]` (positional, e.g. `just
  debug-bench-backends 5`) — microbench the native (godyn) vs bga build backends
  across `cold`/`warm`/`leaf`/`found` edit-locality phases,
  emitting per-build durations to stats-me as `gobuild.conformist.<backend>.<phase>`
  timers (a protocol shared with igloo's dewey bench; uses `nixgc` for cold
  rebuilds). Diagnostic only — not in the CI lane.
- `just run-nix -- <args>` — `nix run . -- <args>`.
- `just bump-version` / `just tag` / `just release` — versioning (release only
  from `master`).

`conformist check` exits 0 when clean, 1 on findings, 2 on operational error.
`conformist --commit` (repair + auto-commit, #24) exits 0 when the tree was
already conformant, 3 when it applied fixes and committed them
(`chore: conformist fmt+fix`, plus any `--trailer` lines — #26), 2 when
refused (dirty tree without `--allow-dirty`, or no git worktree).
`conformist --commit --amend` (#33) folds the run's fixes into HEAD via
`git commit --amend --no-edit` (keeping HEAD's message) instead of a fresh
commit, exiting 3 on amend; it additionally refuses (exit 2) when HEAD has no
commit to amend or is already pushed (`git branch -r --contains HEAD`).
`--exit-zero-on-fix` (#35/#39) exits 0 instead of 3 when fixes were
committed/amended/restaged (refusals/failures stay nonzero), so a caller that
gates on "nonzero = abort" — e.g. a spinclass pre-merge repair hook or a git
pre-commit hook — treats a successful repair as success. It pairs with
`--commit` and with `--staged` (the canonical pre-commit-hook command is
`conformist --staged --exit-zero-on-fix`).
`conformist --staged` (lint-staged restage, #25) exits 0/3/2 analogously:
formats only index-staged files and restages the formatted content, creates
no commit, refuses partially staged files (grep-stable "partially staged"
message token).

## Architecture

### Go program (the engine)

- `main.go` → `cmd.NewRoot(version, commit)`. `version`/`commit` are injected at
  build time by igloo's Go builders — `buildGodynModule` (the godyn default) and
  `buildGoApplication` (bga) both emit `-X main.version` from `version.env` and
  `-X main.commit` from `self.rev`; a plain `go build` leaves them `dev`/`unknown`.
  See `eng-versioning(7)`.
- `cmd/` — cobra commands. `root.go` is the entry point: the bare command
  (`conformist <paths...>`, `ArbitraryArgs`) runs format/repair via `format.Run`,
  or `format.RunCommit` with `--commit` (#24: repair, then commit exactly the
  files the run changed — the pre/post `git status` delta — as
  `chore: conformist fmt+fix`; dirty-tree policy in `commitPreflight`);
  subcommands `check` (`check.go`) and `version` (`version.go`) dispatch
  separately; `conform` (`conform.go` + `cmd/conform/`) scaffolds a repo into the
  eng shape — writes `conformist.nix`/`version.env` if absent (`//go:embed`-ed
  `cmd/conform/scaffold/`), prints the flake.nix + justfile wiring to paste, never
  edits existing files; idempotent, exits 3 when it scaffolds (`ErrScaffolded`);
  a hidden `gen-man` (`genman.go`) renders the section-1 man pages
  from the cobra tree at build time; `--init` writes a starter config via
  `cmd/init`, `--completion` emits shell completions. Config flags live on
  **persistent** flags so `check` inherits tree-root/walk/excludes/config-file.
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
  `check.go` / `repair.go` are the two modes; `sandbox.go` implements the
  copy-and-diff strategy that lets fix-only formatters be _checked_ without
  writing to the source tree (so checks work on a read-only tree); `linter.go`,
  `composite.go`, `glob.go` round out matching and linter execution.
- `walk/` — pluggable tree walkers: `filesystem.go`, `git.go`, `jujutsu.go`,
  `stdin.go`, selected by `type_enum.go`. `walk/cache/` is the bbolt-backed
  (`go.etcd.io/bbolt`) cache: a `paths` bucket for per-file format signatures and
  a `wholetree` bucket for whole-tree check signatures (conformist#16).
- `stats/`, `git/`, `jujutsu/` — run statistics and VCS helpers.
- `test/` — integration harness and fixtures (`test/config`, `test/examples`).
  Fixtures under `test/**` are **deliberately mis-formatted**; they are excluded
  from conformist's own self-lint and must not be reformatted.

### Nix module library (`nix/`)

conformist ships a Nix module like treefmt-nix, extended to cover linters. It is
**self-consumed**: conformist lints/formats its own tree with its own module
(no treefmt-nix dependency — issue #4).

- `nix/default.nix` — the pure library: `evalModule` / `submoduleWith` /
  `mkConfigFile` / `mkWrapper`, plus `mkFormatterModule` (ported ~verbatim from
  treefmt-nix, so `programs/<name>.nix` modules port unchanged) and its linter
  analog `mkLinterModule` (emits `[linter.<name>]` with optional
  `repair-command`/`repair-options`), and `writeCheckScript`
  (`nix/write-check-script.nix`) for packaging a local script as a sandbox-safe
  linter command (`patchShebangs` + wrap, #19). `module-options.nix` declares the
  settings surface and the `build.{wrapper,check,configFile}` outputs.
- `nix/programs/` + `programs.nix` — the formatter registry.
- `nix/linters/` + `linters.nix` — the linter registry. Beyond general linters
  (shellcheck, ruff, statix, deadnix, typos, yamllint, …), this holds the
  **eng-convention enforcers** conformist runs on itself: `eng-versioning`,
  `eng-versioning-deprecated-file` (flags `version.txt` / a flake.nix named
  version var, per eng-versioning(7) "Deprecated alternatives"),
  the justfile checks (`justfile-default`, `justfile-recipe-names`,
  `justfile-debug-recipes` #23, `justfile-recipe-descriptions` — every leaf
  documented, `justfile-task-hierarchy` — pipeline-verb leaves in exactly one
  aggregate, `justfile-leaf-noun` — leaves are verb-noun not bare verbs,
  `justfile-aggregate-comments` — aggregates carry no doc comment; all per
  conformist-justfile(7), the normative home conformist owns for these rules,
  #17), `flake-outputs` and `flake-lock`
  (conformist-nix(7) FLAKE OUTPUTS / FLAKE HYGIENE — outputs formal names all
  inputs, flake.lock is committed; #9/#11), `golangci-dewey`
  (conformist#10: a golangci-lint-gating repo must wire the dewey plugin via
  `.custom-gcl.yml`), `git-remotes`, `git-default-branch`, `sweatfile`,
  `agents-md` (CLAUDE.md→AGENTS.md migration, check + repair).
- `nix/presets/` — reusable rosters a consumer imports to enable the whole
  eng-convention set at once: `eng.nix` (pure: `eng-versioning*`, `flake-*`, the
  seven `justfile-*`) and `eng-impure.nix` (git-state lane: `git-remotes`,
  `git-default-branch`, `sweatfile`, `agents-md`). Exposed as
  `conformist.lib.presets.{eng,eng-impure}`, so a downstream repo's roster is
  `imports = [ conformist.lib.presets.eng ]`. conformist self-consumes them
  (below), so the presets can't drift from what conformist itself runs.
- `nix/conformist.nix` — conformist's own self-config: `imports = [
  ./presets/eng.nix ]` + its formatters, `shellcheck`, the Go-specific
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

- Inputs: `igloo` (amarbel-llc/nixpkgs fork, source of the version-injecting
  `buildGoApplication` **and** `pkgs.go` — the Go toolchain, 1.26.3 — plus the
  `buildGoAuto`/`godyn-gen` native-build tooling and `nixgc` (targeted store GC
  the build-backend bench uses to force cold rebuilds), igloo#29/#28),
  `nixpkgs-master`
  (pinned, source of the devShell Go dev tools `gofumpt`/`golangci-lint`/`gopls`;
  no longer the `go` source), `utils`, and `purse-first` (source of
  `packages.<sys>.golangci-lint-dewey`, the custom golangci-lint carrying dewey's
  analyzers, re-exported as `.#golangci-lint-dewey` for the `lint-go` lane —
  purse-first#134 / conformist#10). **`nixpkgs-master` is the single sha
  source**: igloo's `nixpkgs-master` input follows ours — which only works
  because `pkgs` is `igloo.legacyPackages.<sys>`, NOT the `import igloo {}`
  shim, which reads igloo's committed flake.lock and is follows-immune
  (igloo#37) — and purse-first's `igloo`/`nixpkgs-master`/`utils` follow ours
  too, mirroring eng's follows so the standalone lock matches the build-home
  closure with no duplicate igloo/nixpkgs subtree.
- `packages.{default,conformist}` — **per-system** (`godynSystem` in
  `flake.nix`): on `x86_64-linux`, a `symlinkJoin` of the **godyn (native)**
  binary (`buildGoAuto { strategy = "dev"; }`, `doCheck = false`; integration
  tests need formatter binaries on PATH and run via `just test-go`) and its
  `manpages`; on every other system, the bga join (below). godyn is gated to
  x86_64-linux because that's where it's exercised (godyn(7) LIMITATIONS) and
  because the committed `godyn-graph.json` embeds GOOS/GOARCH-specific file
  lists from `go list` at gen time (e.g. `x/sys/unix`'s `*_linux_amd64`
  sources) — it cannot compile elsewhere until igloo#33 (per-system graphs)
  lands. Building the godyn default requires the `ca-derivations` experimental
  feature — its per-package outputs are content-addressed.
  `packages.conformist-bga` is the ca-derivations-free `buildGoApplication`
  build + man pages (the default off x86_64-linux, opt-in on it; a single
  input-addressed derivation, cold/release-faster — see the backend bench).
  `packages.manpages` is the default backend's man pages alone;
  `conformist-impure-config` is the generated config for `lint-worktree`.
  Self-consumption evals (`nix fmt` / `checks.formatting`) use the bare
  default-backend binary (`selfBin`), not the join.
- `packages.conformist-native` — the **bare** godyn binary (the default's backend
  without man pages), for the fast edit loop and the backend bench
  (`.#conformist-native.passthru.bga` is the bga build buildGoAuto keeps reachable).
  `buildGoApplication`-only knobs (`subPackages`, `GOTOOLCHAIN`) pass through
  `bgaArgs`; `go = pkgs.go` keeps both backends on one compiler. `godyn-graph.json`
  is the committed Go dependency graph (regenerated by `just build-godyn-graph`,
  drift-gated by `just verify-godyn-graph`); it captures `cmd/init`'s `//go:embed`.
  See igloo#29 / `man 7 godyn`.
- **Man pages** (`doc/`, `eng-manpages(7)`): hand-written scdoc for sections
  2–9 (`doc/conformist.toml.5.scd`, `doc/conformist.7.scd`,
  `doc/conformist-nix.7.scd` — the normative home for the `flake-*` linters'
  conventions, `doc/conformist-justfile.7.scd` — likewise for the `justfile-*`
  linters, citing eng-design_patterns-justfile(7) as prose origin) plus the
  codegen
  section-1 reference via `conformist gen-man`, all compiled by the `manpages`
  Nix derivation — the build is the man-page lint (PRINCIPLE 4), there is no
  justfile recipe. Note `doc/` (man-page sources) is distinct from `docs/`
  (the mkdocs prose site).
- `formatter` (= `nix fmt` wrapper), `checks.formatting` (sandboxed read-only
  gate) + the `formatter-*`/`linter-*` registry smoke tests.
- `lib` = the Nix module library (`conformist.lib.evalModule pkgs { … }`), which
  also carries `lib.presets.{eng,eng-impure}` (the one-import eng rosters, see
  `nix/presets/`); `flakeModule` = `flake-module.nix` (flake-parts
  `perSystem.conformist`).
- `templates.eng` (`templates/eng/`) — `nix flake init -t
  github:amarbel-llc/conformist#eng` scaffolds an adopter repo wired to the eng
  preset (flake.nix + conformist.nix + a conformist-justfile(7)-conformant
  justfile + version.env + .envrc). `templates/**` is excluded from conformist's
  own self-lint (consumer-facing scaffold, e.g. a direnv `.envrc` has no
  shebang); the `explore-template-eng` recipe smoke-tests instantiation.
  Downstream consumers MUST set `conformist.package` — conformist is not in
  nixpkgs, so the module's `package` option has no default.

## Conventions and gotchas

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
- `docs/` (mkdocs site + RFCs) is prose and is excluded from code formatters; do
  not expect `nix fmt` to touch it.
