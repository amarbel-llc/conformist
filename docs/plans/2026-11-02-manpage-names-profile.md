# Plan: the manpage NAME-line lint in the conformist profile (conformist#114)

Date: 2026-11-02. Tracker: conformist#114.

Update, same day: doppelgang v0.2.0 published `doppelgang-static-x86_64-linux`.
`conformist.profile` now pins it and carries `[linter.manpage-names]`, with a
pass/fail control in `just explore-profile-check` and `just
explore-doppelgang-binary`. Remaining: re-sign and serve the profile (circus),
the per-repo work below, and the rendered-page check. The Blocker section below
is the state before the release. Trap found while wiring: a profile command is
always a shell line (the resolver appends a pin comment), so a `passes-files`
stanza must write `"$@"` or the matched files are silently dropped.

Rendered-page check (section 3): done as `lib.mkManpageNamesCheck`
(`nix/mk-manpage-names-check.nix`), the fixed-output-derivation option. conformist
runs it over its own `manpages` as `checks.<sys>.manpage-names`; the regression is
`checks.<sys>.mk-manpage-names-check`. The Go template already carries a
`manpage-names` check using a doppelgang flake input; the helper is the
input-free form. aarch64-linux: v0.2.0 ships no such asset; doppelgang was asked whether a later
release does.

## Why now

eng's pre-merge gate ran `doppelgang lint-man` over the aggregated first-party
manpath (581 pages) and failed on three pages owned by other repos
(purse-first `dagnabit.1` and `dewey-analyzers.7`, spinclass
`spinclass-grit.7`), blocking an unrelated eng merge. Decision: eng stops
gating on the aggregate; the NAME-line check is enforced per repository through
the conformist eng-* profiles so offenders are fixed where they are introduced.

## Blocker

The profile pins every tool as an `[artifact.*]` release asset by markl digest
(`conformist.profile`, RFC 0005 §2). `linenisgreat/doppelgang` has no forge
release and no asset-publishing machinery, so there is nothing to pin. A stanza
with an invented digest fails closed (operational error, exit 2: RFC 0005 §2),
so it cannot be landed ahead of the release; landing it behind a gate would
only ship dead config.

## What exists (evidence)

doppelgang (read-only checkout; Go, version.env `0.1.1`):

- `doppelgang lint-man [--max N] [--format auto|text|json]
  <manpath-root|page|file.scd>...` exists (`cmd/doppelgang/main.go`, package
  `internal/alfa/manlint`); `--max` defaults to `manlint.DefaultMaxDescription`.
  It takes rendered pages (`.gz` allowed), a manpath root, or scdoc sources, so
  one binary serves both modes. `lint-man` does not shell out; only `lint`'s
  closure/transitive paths exec `nix`.
- The package is `buildGoApplication` with `CGO_ENABLED=0`
  (`flake.nix` `doppelgangBin`), so the binary is already a pure-Go,
  non-libc executable on x86_64-linux. It has not been checked for surviving
  `/nix/store` references (conformist-static uses `allowedReferences = []` for
  that). Open question, to be verified by the doppelgang session.
- Its justfile has `tag`, `bump-version` and `release` (signed tag, push) but
  no forge release and no asset step; `.github/` workflows exist but are not a
  publishing path. No `v*` tags were checked beyond the issue's statement.

Precedents for publishing assets:

- conformist: `packages.conformist-static` (flake.nix), `just deploy-release`
  / `deploy-release-assets` (tag the merged commit, `release/vX.Y` line,
  `smith release create`, `smith release asset create` built from the TAG, idempotent),
  and the `release` `[[post-merge]]` target in `sweatfile`. This is the one to
  copy: doppelgang is Go and uses the same forge/smith flow.
- just-us: `justStatic` and `justStaticAarch64Linux` (pkgsStatic, and
  `pkgsCross.aarch64-multiplatform`) exposed as `packages`; assets uploaded as
  `just-static-<triple>` (upload is manual: its AGENTS.md says the fork's CI
  does not ship artifacts). Cross-building aarch64 for Go: conformist found
  its aarch64 cross link falls back to the host gcc even with cgo off, so
  aarch64 is unproven for doppelgang too.

conformist profile mechanics (RFC 0005, `conformist.profile`):

- `[artifact.<name>]` with `form = "static"`, per-system tables
  `[artifact.<name>.system.<system>]` (`url`, `markl`); pin purpose
  `conformist-artifact-digest-v1`, sha256 or blake2b256, computed from the
  bytes the URL serves via `just explore-markl-pin <url>`. Linux only.
- Executable artifacts are placed on PATH by bare name, so the stanza says
  `doppelgang`.
- A repo inherits by copying the profile verbatim or by delegation (RFC 0005
  §3.1, upward walk, nearest layer wins). Run via
  `conformist check --profile <file> --profile-only` (EXPERIMENTAL; falls back to
  `$CONFORMIST_PROFILE` / `$CONFORMIST_PROFILE_KEYS`).
- Known limits: the profile executes per-file linters over `includes` globs and
  has no way to depend on a build output.

## Dependency chain

1. **doppelgang release** (doppelgang session): publish per-system static
   binaries as forge release assets.
2. **conformist profile stanza** (conformist): add the artifact and linter,
   pinned by markl, in the same change as the profile re-sign/serve.
3. **repos inherit**: each repo picks up the new profile and fixes its
   offenders.
4. **eng**: drops the aggregate `validate-manpage-names` from its gate.

Step 4 may happen immediately (the operator has already decided eng stops
gating); steps 1 to 3 are what restore enforcement.

## 1. doppelgang: smallest change (request for a doppelgang session)

1. `flake.nix`: add `packages.doppelgang-static`, the same `buildGoApplication`
   as `doppelgangBin` plus `ldflags = ["-s" "-w"]`, tags `netgo osusergo`,
   `allowedReferences = []` (fail the build if a store path survives), as
   `conformist-static` does. x86_64-linux first.
2. `justfile`: copy conformist's `deploy-release` and `deploy-release-assets`
   (substituting repo `linenisgreat/doppelgang`, `DOPPELGANG_VERSION`, binary
   `doppelgang`, asset `doppelgang-static-x86_64-linux`) and add a
   `bump-version-level` recipe.
3. `sweatfile`: add the `release` `[[post-merge]]` target (and
   `post-merge-timeout`) from conformist's sweatfile.
4. Bump `DOPPELGANG_VERSION` to a release (0.1.2 or 0.2.0, whichever contains
   `lint-man`; the checkout is at 0.1.1) and merge so the target creates the
   tag, forge release and asset.
5. Optional second step: `aarch64-linux`, via `pkgsCross.aarch64-multiplatform`
   (just-us shape). Not required for the first profile (assumption, to confirm
   with the operator: the profile gate hosts are x86_64-linux); an aarch64 host
   gets the documented "no build for this system" error.

Report back to conformist: asset URLs per system.

## 2. conformist: the stanza (once the URLs exist)

Compute each pin from the served bytes: `just explore-markl-pin <url>`.

```toml
[artifact.doppelgang]
form = "static"

[artifact.doppelgang.system.x86_64-linux]
url = "https://code.linenisgreat.com/doppelgang/releases/download/vX.Y.Z/doppelgang-static-x86_64-linux"
markl = "conformist-artifact-digest-v1@sha256-<computed>"

# [artifact.doppelgang.system.aarch64-linux] only when step 1.5 ships it.

# conformist-manpage-names: the index-builder contract on NAME lines (spinclass
# FDR 0030): `name - description`, non-empty, one physical line, at most 72
# characters. Source level; rendered pages are the flake check below.
[linter.manpage-names]
command = "doppelgang lint-man --max 72"
includes = ["*.scd"]
```

Notes to verify when applying: the exact path form `includes` needs to match
`doc/**/*.scd` (see conformist.toml(5) GLOB PATTERNS); `passes-files` stays the
default true since `lint-man` accepts several `*.scd` arguments; fixtures under
`test/**` that are deliberately wrong must be excluded. The stanza would carry
no rule/prelude, so no `rule-tool` is needed. The linter is read-only (no
`repair-command`): the fix is editorial.

Add a profile-gate control as `explore-profile-check` has for the justfile
linters: a tmp tree with a one-line-too-long NAME must exit 1, a conformant one
exit 0, so the stanza cannot pass for an incidental reason (AGENTS.md
Conventions).

## 3. Rendered-page mode (Go-codegen repos)

Repos whose pages come from Go man-page codegen (conformist's `gen-man`, and
per the issue the MCP-tool-description pages of other Go repos) have no `.scd`
file for the long descriptions. The profile cannot see a build output. Two options:

- **Flake check emitted by the module** (recommended): a `lintManpages`
  check in `nix/` (`presets/eng.nix`, or the registry via
  `mkLayoutCheck`) that runs `doppelgang lint-man ${pkg}/share/man` over a
  consumer-named package. This needs doppelgang as a nix input or the same
  released static binary fetched as a fixed-output derivation (the markl digest
  doubles as the FOD hash). Prefer the FOD so conformist keeps no flake edge to
  doppelgang (conformist already declines a purse-first input to avoid cycles).
- A `just lint-man` recipe per repo (doppelgang's own `lint-man` shape),
  hard-coding the package. Cheaper but not inherited.

Decide at step 2; the source-level stanza does not depend on it.

## 4. What each repo does

| Repo | Action |
|---|---|
| doppelgang | steps 1.1 to 1.4; keep its own `lint-man` recipe |
| conformist | add the artifact and stanza, the control, `conformist-testing(7)` / `conformist.toml(5)` mention, pin refresh recipe; fix its own NAME lines if flagged (its pages are `.scd` plus `gen-man`) |
| purse-first | fix `dagnabit.1` and `dewey-analyzers.7`; inherit the profile; add the rendered-page check |
| spinclass | fix `spinclass-grit.7`; inherit the profile; add the rendered-page check |
| circus | already consumes the profile (justfile linters); pick up the new stanza when the profile is re-signed and served |
| eng | drop the aggregate `validate-manpage-names` pre-merge gate (decided); keep it as a non-blocking report if wanted |

## Order

1. doppelgang session: 1.1 to 1.4, hand back asset URLs.
2. conformist: compute pins, add the stanza and control, re-sign and serve the
   profile (circus owns signing and serving from its conformist input).
3. purse-first and spinclass: fix the three pages (can happen any time; the
   lint is satisfied by the fix regardless of the profile).
4. repos pick up the profile; rendered-page check in Go-codegen repos.
5. eng drops the aggregate gate (independent; may land first).

## Open questions

- Does the doppelgang Go binary survive `allowedReferences = []`?
- aarch64-linux static Go cross build (conformist's link fails; unverified for
  doppelgang).
- `includes` spelling for `.scd` at any depth.
- Fixed-output derivation versus flake input for the rendered-page check.
