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

- `just` (= `just default` = `validate build test verify lint`) — full local CI
  lane; the merge hook runs the devshell `validate` gate and the Go test suite
  (`test`) too.
- `just build` — `build-gomod2nix` + `build-go` + `build-nix`. (The opt-in
  godyn backend's graph regen is no longer in this lane — it's now the
  debug-grouped `debug-godyn-graph`, see below.)
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
  `.git` is available) + `lint-go` (golangci-lint carrying the dewey analyzers,
  built locally from a pinned purse-first source fetch — see the flake inputs
  note below; `.golangci.yaml` is `default: all` minus a curated disable list,
  plus the `dewey` custom linter — conformist#10/#22).
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
refused (dirty tree without `--allow-dirty`, or no git worktree). Before
committing in any `--commit` mode it also refuses (exit 2) when the
to-be-committed content carries leftover merge-conflict markers
(`<<<<<<<`/`=======`/`>>>>>>>`, or diff3 `|||||||`) — detected via
`git diff --check` over the files it would commit — rather than burying a
non-building commit (especially via `--amend`) in history (#67); a conflict is
not a fixable issue, so `--exit-zero-on-fix` (below) never swallows it.
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
`conformist --staged` (lint-staged restage, #25/#40) exits 0/3/2 analogously:
formats only index-staged files and restages the formatted content, creating
no commit. A fully-staged file is formatted in the working tree and `git add`ed;
a partially staged file (staged with additional unstaged edits) is no longer
refused — its STAGED blob is formatted in isolation and restaged via the object
store (`git hash-object` + `git update-index --cacheinfo`), leaving the working
tree's unstaged hunks untouched (#40). A whole-tree codegen-repair linter
(`passes-files=false` + `repair-command`) that sets `restage-repair-outputs`
also has the (tracked) files its repair regenerates restaged — even when they
were never staged — detected by a git-status delta taken around that linter's
repair, so a stale generated sibling does not strand the commit (#55). Adding
`stage-new-outputs` (tier 3, #56) additionally stages the brand-new (untracked)
files such a linter's repair creates — the delta is then taken with
`--untracked-files=all`; it is a distinct opt-in because staging untracked files
is more dangerous, so `restage-repair-outputs` alone never stages them. Adding
`stage-deleted-outputs` (tier 4, #57) additionally stages the deletions such a
linter's repair performs (e.g. a package-move codegen removing a relocated
file); it is the most destructive mutation, so it too is a distinct opt-in and
the default now excludes deletions from the restage set (tiers 2–3 never stage a
removal). It still refuses (exit 2) outside a git worktree, in stdin mode, or
under fail-on-change.

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
  resolved config/toolchain identity hash, conformist#76) and `version`
  (`version.go`) dispatch
  separately; `conform` (`conform.go` + `cmd/conform/`) scaffolds a repo into the
  eng shape — writes every absent shape file (`conformist.nix`, a `version.env`
  whose key is derived from the repo name — git origin remote, else the directory
  basename, via `git.OriginRepoName` — and, greenfield, a complete
  `flake.nix`/`justfile`; all `//go:embed`-ed from `cmd/conform/scaffold/`, the
  flake.nix/justfile kept byte-identical to `templates/eng/` by a drift test —
  #41). An existing `flake.nix` that is the recognized `eachDefaultSystem` shape
  is edited **in place** to splice the `conformist` and `just-us` inputs (the
  latter supplying both the devShell's `just` and the
  `justfile-orphan-summary` linter module) and the per-system outputs wiring
  (`cmd/conform/flakeedit/`, #61); any other shape (or
  `--no-edit`) falls back to printing the wiring to paste, and an existing
  justfile is never edited (its recipes are printed). The shared PEG
  infrastructure — grammars (`nix.peg`, `outputs.peg`), navigation helpers,
  splice types, and `ParseFlake` — lives in `cmd/conform/flakeparse/` (modelled
  on amarbel-llc/doppelgang's `nixedit`); flakeedit imports it for its
  wiring-specific logic. flakeedit splices by byte offset so the rest of the
  file is preserved verbatim; it is per-target
  idempotent and never clobbers an output attr it did not write. An existing
  `devShells.default` is merged into (conformist's tools spliced into its
  `packages` list) and an existing `formatter` is replaced only under
  `--force-formatter`; otherwise a pre-existing attr is reported as a conflict
  to reconcile by hand (#63). `just verify-flakeedit-parse` (in the `verify`
  lane) runs `conform` over the `test/flakeedit/` fixtures and
  `nix-instantiate --parse`s each rewrite, so a splice regression that yields
  unparseable Nix fails CI. A `flake.nix`/`justfile` that ALREADY carries the
  conformist wiring is detected (conformance sentinels: a justfile `lint-fmt`
  recipe / `checks.${system}.formatting`; a flake referencing
  `conformist.lib.evalModule`) and left silent instead of nagging with the paste
  snippet (#42(i)). To finish converging a brownfield tree, `conform` prints the
  single `RepairCommand` — `nix fmt` (pure formatter + file-linter repair) then
  the eng-impure lane's linters (`agents-md`, `gomod2nix`, …) in repair mode over
  the working tree — that delegates the real content edits to conformist's own
  linters; `conform --repair` runs that SAME command inline (working-tree only,
  NO commit, leaving changes for the operator to review — the adoption-wave
  zero-action path), the emitted and executed forms being one string
  (`cmd/conform/conform.go`, #42(ii)). Idempotent
  overall, exits 3 when it wrote/edited/repaired files (`ErrScaffolded`), 0 when
  the tree is already conformant. `conform <domain>` / `conform <domain>#<id>` is
  a distinct mode (#43): it resolves a flake template advertised by that domain's
  PAPI document (`cmd/conform/papi/` — fetch `https://<domain>/.well-known/papi`,
  follow `resources.templates` within the operator's own DNS tree, read the
  visible `templates[]` of the `{data,meta}` collection per PAPI RFC-0001 §7/§8),
  surfaces the resolved `flakeref`, and runs `nix flake init -t <flakeref>` (a
  bare domain with one template uses it, several prompts via `huh` on a TTY and
  otherwise fails listing the ids, refusing to guess); it maps operational
  failures to exit 2 (`ErrConformFailed`) and refuses a non-empty target without
  `--overwrite`.
  A hidden `gen-man` (`genman.go`) renders the section-1 man pages
  from the cobra tree at build time; `--init` writes a starter config via
  `cmd/init`, `--completion` emits shell completions. Config flags live on
  **persistent** flags so `check` inherits tree-root/walk/excludes/config-file.
- `cmd/flakeclobber/` — a separate binary for fleet migration (RFC 0004,
  conformist#99/#100). Applies targeted list-element replacements in
  `devShells.default.packages` across a fleet of repos (e.g. `pkgs.just` →
  `justPkg`). Shares the PEG infrastructure from `cmd/conform/flakeparse/`.
  Dry-run by default (`--apply` to write); verifies each rewrite with
  `nix-instantiate --parse` before writing to disk. Exit codes: 0 = success
  (including already-migrated), 1 = one or more files had migration errors, 2 =
  operational error (bad flags, I/O failure). Do NOT wire into `conform` — it is
  an intentionally separate one-shot sweep tool.
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
  `repair-command`/`repair-options`), `writeCheckScript`
  (`nix/write-check-script.nix`) for packaging a local script as a sandbox-safe
  linter command (`patchShebangs` + wrap, #19), `wrapWithToolchain`
  (`nix/wrap-with-toolchain.nix`) for a single conformist wrapper carrying its
  formatter toolchain on PATH — the non-module hermetic `nix fmt`/`--staged` hook
  for a repo with a hand-written `conformist.toml` (#51), and `mkToolchainHooks`
  (`nix/mk-toolchain-hooks.nix`) which returns the three named wrappers
  `{ formatter, preCommit, repair }` (named `conformist` /
  `conformist-pre-commit` / `conformist-repair`) — the TOML-consumer mirror of
  the module's `build.{wrapper,preCommit,repair}`, so a hand-written-config repo
  wires its hooks 1:1 with how a module adopter does (#59), and
  `mkTomlFormat`/`mkYamlFormat` — remarshal-free replacements for
  `pkgs.formats.toml`/`pkgs.formats.yaml` (whose `.generate` serializes via
  remarshal, dragging `matplotlib`→`ffmpeg` into EVERY generated config as a
  build-time dep — #60). They keep `pkgs.formats.<fmt>.type` for value validation
  and swap `.generate` for a `yj` json→toml/yaml step; passed to all modules via
  `defaultSpecialArgs` so every TOML/YAML config generator (the conformist config
  itself plus statix/stylua/taplo/yamllint/… settings files) is remarshal-free.
  `just verify-no-remarshal` guards against new direct `pkgs.formats.{toml,yaml}`
  uses creeping back in. `module-options.nix`
  declares the settings surface and the
  `build.{devShell,configFile,wrapper,preCommit,repair,programs,check}` outputs.
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
  `.custom-gcl.yml`), `git-remotes` (SSH-only remotes AND a canonical `origin`
  host — check reports any non-SSH remote plus an `origin` whose host isn't
  `canonical-host` (default `code.linenisgreat.com`, the forge) or one of a
  per-repo `allowed-hosts` allowlist (e.g. `[ "github.com" ]` for a repo
  deliberately still on GitHub); repair rewrites github.com/
  code.linenisgreat.com https/http/git remotes to SSH regardless of that
  allowlist — #68),
  `git-default-branch`, `sweatfile`,
  `agents-md` (CLAUDE.md→AGENTS.md migration, check + repair), `gomod2nix`
  (conformist-nix(7) GO MODULE LOCK — gomod2nix.toml in sync with go.mod/go.sum;
  check regenerates-to-temp + diffs, repair regenerates + `git add`s; impure
  because regen needs the module graph and repair stages; watches the
  default-excluded go.mod/go.sum — a whole-tree check (`passes-files=false`) is
  exempt from the global excludes by design, its includes being a trigger gate
  (conformist#45, retiring the conformist#44 `ignore-global-excludes` flag);
  native check pending amarbel-llc/gomod2nix#14). `clippy` (conformist#69 — a
  first-class Rust lint: check is `cargo clippy … -- -D warnings`, repair is
  `cargo clippy --fix`; whole-tree, `restage-repair-outputs`. **Impure** (it
  compiles the crate) so it's working-tree-lane only, and **opt-in**: it is a
  registered module — enable with `linters.clippy.enable = true` — but is NOT in
  the eng-impure preset roster, so a non-Rust repo never pulls a Rust toolchain.
  conformist pins NO Rust: the `packages` toolchain defaults to
  cargo/clippy/rustc/gcc from the consumer's own nixpkgs (overridable for
  rust-overlay/fenix). Knobs: `manifest-path`, `workspace`, `all-targets`,
  `extra-args`, `deny`, `allow`. Behavioral fixtures live in a separate
  `clippy-fixtures` aggregate built by `just explore-clippy-fixture`, kept out of
  the verify/CI lane so CI stays Rust-free).
- `nix/presets/` — reusable rosters a consumer imports to enable the whole
  eng-convention set at once: `eng.nix` (pure: `eng-versioning*`, `flake-*`, the
  seven `justfile-*`), `eng-go.nix` (the canonical Go formatter chain: `goimports`
  priority 1 then `gofumpt` priority 2 — the sequence the fleet converged on,
  eng #18; kept separate from `eng` so a non-Go repo never pulls a Go toolchain),
  and `eng-impure.nix` (git-state lane: `git-remotes`, `git-default-branch`,
  `sweatfile`, `agents-md`, `gomod2nix`). Exposed as
  `conformist.lib.presets.{eng,eng-go,eng-impure}`, so a downstream repo's roster
  is `imports = [ conformist.lib.presets.eng conformist.lib.presets.eng-go ]`.
  conformist self-consumes them (below), so the presets can't drift from what
  conformist itself runs.
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

- Inputs: `igloo` (amarbel-llc/nixpkgs fork, source of the version-injecting
  `buildGoApplication` **and** `pkgs.go` — the Go toolchain, 1.26.3 — plus the
  `buildGoAuto`/`godyn-gen` native-build tooling and `nixgc` (targeted store GC
  the build-backend bench uses to force cold rebuilds), igloo#29/#28),
  `nixpkgs-master`
  (pinned, source of the devShell Go dev tools `gofumpt`/`golangci-lint`/`gopls`;
  no longer the `go` source), and `utils`. **conformist deliberately does NOT
  take `purse-first` as a flake input** — it must stay strictly upstream of
  purse-first (no cycle). It still dogfoods purse-first's dewey golangci-lint
  plugin on its own Go (`.#golangci-lint-dewey`, the `lint-go` lane —
  purse-first#134 / conformist#10), but consumes it as a **fixed-output source
  fetch** (`golangciLintDeweySrc`: `fetchFromGitHub` pinned by rev + hash) and
  builds the binary itself via `buildGoApplication` (the recipe ported from
  purse-first's `gomod.nix`, ldflags trimmed to `-s -w` so the output is
  reproducible across purse-first commits). An FOD leaf pins source by commit
  and pulls no flake graph, so purse-first may import conformist without closing
  a loop; bump the rev+hash deliberately to track the plugin. **`nixpkgs-master`
  is the single sha source**: igloo's `nixpkgs-master` input follows ours —
  which only works because `pkgs` is `igloo.legacyPackages.<sys>`, NOT the
  `import igloo {}` shim, which reads igloo's committed flake.lock and is
  follows-immune (igloo#37).
- `packages.{default,conformist}` — on **every** system, the **bga** join: a
  `symlinkJoin` of the `buildGoApplication` binary + its `manpages` (`manpagesBga`).
  Platform-agnostic, ca-derivations-free, no per-system graph. `packages.conformist-bga`
  names the same build explicitly (a stable name for the bga-vs-native A/B and the
  backend bench). `packages.manpages` is the bga backend's man pages alone;
  `conformist-impure-config` is the generated config for `lint-worktree`.
  Self-consumption evals (`nix fmt` / `checks.formatting`) use the bare bga
  binary (`selfBin`), not the join.
- `packages.conformist-native` — the **opt-in** godyn (native) backend: the bare
  binary (`buildGoAuto { strategy = "dev"; }`, `doCheck = false`; no man pages),
  plus the man-page-bundled `conformist` join, for the fast edit loop, the backend
  bench (`.#conformist-native.passthru.bga` is the bga build buildGoAuto keeps
  reachable), and future godyn work. **NOT the default** — bga is, on all systems.
  godyn is gated to x86_64-linux (`godynSystem`) because the committed
  `godyn-graph.json` embeds GOOS/GOARCH-specific file lists from `go list` at gen
  time (e.g. `x/sys/unix`'s `*_linux_amd64` sources) — it cannot compile elsewhere
  until igloo#33 (per-system graphs) lands — and because building it requires the
  `ca-derivations` experimental feature (its per-package outputs are
  content-addressed). The JSON is a hand-committed, redundant restatement of
  `gomod2nix.toml` that drifts from source (it broke the Linux build when
  `cmd/conform` gained `//go:embed` patterns the graph didn't list); demoting it
  to opt-in is why bga is the default. `buildGoApplication`-only knobs
  (`subPackages`, `GOTOOLCHAIN`) pass through `bgaArgs`; `go = pkgs.go` keeps both
  backends on one compiler. The graph is regenerated by the **opt-in**
  `just debug-godyn-graph` (x86_64-linux only) and drift-checked by
  `just debug-godyn-graph-drift` — both debug-grouped, neither in the default
  `build`/`verify` lane anymore (godyn is opt-in, so its graph regen/drift recipes
  are no longer pipeline-verb leaves; conformist's own `justfile-task-hierarchy`
  linter requires pipeline-verb leaves to sit in an aggregate, which these no
  longer do); it captures the `//go:embed` patterns (`cmd/conform/scaffold/*`,
  `cmd/init/init.toml`). See igloo#29 / `man 7 godyn`.
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
  also carries `lib.presets.{eng,eng-go,eng-impure}` (the one-import eng rosters,
  see `nix/presets/`); `flakeModule` = `flake-module.nix` (flake-parts
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
