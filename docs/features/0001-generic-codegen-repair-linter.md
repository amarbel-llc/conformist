---
status: experimental
date: 2026-09-27
promotion-criteria: |
  experimental -> testing: at least two fleet repos have a check carrying
  passthru.codegenPatch and run this lane in their pre-commit hook for two weeks,
  with no commit blocked by a refusal that turned out to be spurious.

  testing -> accepted: passthru.codegenPrefix exists upstream and is honored here
  (closing the subdirectory-module limitation), the trigger globs have needed no
  adjustment for two weeks, and no repo has had to disable the lane.
---

# Generic codegen-repair at commit time

## Problem Statement

A generated file goes stale the moment its producer changes, and today that is
discovered at the merge gate — after the commit, by the consuming repo's own drift
check. The author then has to recognize the failure, find that repo's particular
regenerate incantation, and amend. There is no single command to run, because every
generator is different: a tommy config header, a dagnabit facade export, a lockfile
bump's downstream stamp. What the repos DO share is that each generator already
ships a flake check that fails when its output is stale — so the drift is already
detected mechanically, just too late and with no attached remedy.

## Interface

### What it discovers

`conformist codegen-repair` enumerates `checks.<system>` in the repo's flake and
keeps every attr carrying `passthru.codegenPatch` (the igloo#80 contract):

| Attribute | Meaning |
|---|---|
| `passthru.codegenPatch` | A derivation whose `$out/patch` is a `git diff --no-index --binary src work`, empty when the tree is already current. Its presence is what marks a check as repairable. |
| `passthru.codegenPrefix` | Optional. The tree-root-relative directory the patch is rooted at (the module root given to the builder as `src`). Absent or empty means the tree root. |
| `passthru.codegenIncludes` | Optional. Trigger globs the generator declares for inputs that are not Go sources. Reported, not acted on — see Limitations. |

Nothing else is read, and no generator is named anywhere: a repo onboards by having
a check with that passthru, with no conformist change and no consumer flake edit.
The only host requirements are `nix` (resolved from PATH, so the host's own store,
substituters and eval cache are used) and `git`.

### The subcommand

    conformist codegen-repair [--flake REF] [--system DOUBLE] [--nix PATH]
                             [--tree-root DIR] [--strict] [--list]

It applies one patch, re-discovers, and rebuilds the rest against the updated tree,
repeating until every patch comes back empty. `--list` prints the discovered checks
as JSON and applies nothing. It loads no `conformist.toml`: its whole input is the
flake and the worktree, which is also what lets the same code ship as a standalone
static artifact.

| Exit | Meaning |
|---|---|
| 0 | Converged, or nothing to do, or a soft failure was warned about |
| 2 | A built patch would not apply; the run would not converge; or, under `--strict`, a discovery or build failure |

Discovery and per-check build failures WARN and exit 0. This runs inside a git
pre-commit hook, where a non-zero exit blocks the commit, so an unreachable or
flaky nix must not stop a repo from committing — the drift check is still the gate,
and the worst case is the staleness the lane was trying to pre-empt. A patch that
was BUILT but will not apply is different: the contract says it was generated from
this very tree, so a refusal means something is genuinely inconsistent, and that
fails. `--strict` promotes the soft failures for a lane that would rather stop.

**Those exit codes and warnings are only visible when the command is run
directly.** See "Signals that do not reach the operator yet" under Limitations —
through the linter wiring, both are currently swallowed.

### The linter

`linters.codegen-repair` wires the subcommand as a whole-tree
(`passes-files = false`) linter's `repair-command`. It is enabled by
`conformist.lib.presets.eng`, so an eng repo has the lane without configuring
anything, and a repo whose checks carry no `codegenPatch` gets a no-op.

| Option | Default | Meaning |
|---|---|---|
| `enable` | false (true via `presets.eng`) | |
| `package` | `null` | The conformist whose subcommand runs. Null resolves `conformist` from PATH; `presets.eng` sets it from the module's own `package`, giving a hermetic store path. |
| `strict` | `false` | Pass `--strict`. |
| `includes` | `[ "*.go" "flake.lock" "go.nix" ]` | Fire-trigger globs. |
| `extra-includes` | `[ ]` | Appended to `includes`. |

The read-only `command` is a deliberate no-op: the drift check that carries the
passthru is already the check-mode gate, so running a second copy of it here would
double-report every finding, pay for a second build, and leave two places to debug.
That is also what makes the lane safe inside the PURE `eng` preset despite needing
live nix and git — check mode never invokes a repair.

All three staging tiers are on (`restage-repair-outputs`, `stage-new-outputs`,
`stage-deleted-outputs`), because a codegenPatch carries rewritten, created and
deleted files and the whole point is that they land in the commit that triggered the
repair. Attribution is per-linter — the hook takes a git-status delta around THIS
linter's repair — so enabling the dangerous tiers here does not widen what any other
linter may stage.

## Examples

A pre-commit hook absorbing a stale generated file. The author edits a source and
commits, having forgotten to regenerate:

    $ git add internal/config.go
    $ git commit -m 'add a field'
    # the hook runs `conformist --staged --exit-zero-on-fix`, which:
    #   - matches internal/config.go against the linter's `*.go` trigger
    #   - builds checks.x86_64-linux.tommy-codegen.passthru.codegenPatch
    #   - applies it, restamping config_tommy.go
    #   - stages config_tommy.go alongside the author's own edit
    # the commit lands self-consistent, and the repo's drift check passes on it

Seeing what a repo exposes, and what each check wants triggered:

    $ conformist codegen-repair --list
    [
      {
        "name": "tommy-codegen",
        "prefix": "",
        "includes": [ "*.toml" ]
      }
    ]

Repairing by hand, failing loudly rather than warning:

    $ conformist codegen-repair --strict

## Limitations

**Repair-only.** `conformist check` does not run this. The repo's own drift check
reports staleness; this only fixes it.

**Signals that do not reach the operator yet.** Run directly, the command exits 2
on a patch that will not apply and warns on a soft failure. Run as a linter's
`repair-command` — which is how the lane actually runs — both are swallowed by
plumbing that predates this feature:

- `format.Linter.Repair` discards the non-zero-exit flag `invocation.run` returns
  (`format/exec.go` maps a non-zero exit to `nonzero=true, err=nil`), so the exit 2
  never becomes an error and the commit proceeds.
- The same function logs the command's captured output at `Debug` while the default
  log level is `Warn`, so every warning needs `-vv` to appear — and no hook passes
  `-vv`.

The consequence is not an unsafe tree: a patch that will not apply leaves the
generated file stale, which the drift check still catches at the merge gate. The
consequence is that the lane is *quieter than documented*, and that
`linters.codegen-repair.strict = true` cannot fail a hook — it converts soft
failures into the same swallowed exit 2. Making either effective means changing how
conformist surfaces repair-command exits and output for ALL linters (a non-zero
`cargo clippy --fix` with unfixable lints left over is deliberately not fatal
today), which is a conformist-wide decision rather than part of this feature.

**A module root in a subdirectory is refused, not repaired.** A codegenPatch's paths
are relative to its check's module root, which may be a repo subdirectory (a repo
whose Go module lives under `go/`). `passthru.codegenPrefix` is read and honored as
`git apply --directory` when present, so the field can land upstream without another
change here — but until it exists, such a patch is REFUSED with a message naming the
absent paths and the field that would fix it. Applying it at the tree root would be
a silent wrong-prefix write, which is the worst outcome available. Tracked on
igloo#80.

**That refusal is not airtight for an add-only patch.** The refusal works by noticing
that the patch targets files absent from the tree root, which a patch that only
CREATES files does not reveal — its targets are legitimately absent either way. A
subdirectory-rooted check whose patch only adds files would therefore create them at
the repository root. Nothing in the patch distinguishes that case, so
`codegenPrefix` is the only real fix; inferring the module root from the repo's
layout was considered and rejected, because a repair tool that guesses at a root is
how a repository gets corrupted.

**Trigger globs are not unioned with `codegenIncludes` automatically.** This module
is evaluated INSIDE the consumer's own flake, so reading `checks` from here to learn
what the generators want triggered would recurse through `checks.formatting`. The
subcommand logs each check's declared `codegenIncludes` instead, and
`extra-includes` is where to add them by hand. No glob-coverage warning is emitted:
deciding whether one glob set covers another is not decidable in general, and a
check that fires for an incidental reason is worse than no check.

**Drift is only what git can carry.** An empty directory is not drift, and file
modes are not compared (igloo#80 note 6). A generator whose output differs only in
those ways will not be repaired here.

**A check whose attr name needs quoting in a nix attrpath is skipped** with a
warning, rather than guessing at nix's quoting rules in a fleet-wide commit hook. No
check in the fleet needs it today.

**Not wired into any RFC 0005 profile.** Delivery is built-in now, plus the existing
`packages.conformist-static` release artifact (x86_64-linux; darwin artifacts are
deferred). Moving the lane into a papi-served profile is held until POC v2
deliberately.

## Discovery cost

The design review left one question open: what does the discovery eval cost on a
DIRTY tree versus a clean one, and does nix's eval cache apply? That decides whether
the fire-triggers can later be relaxed toward "run on every commit". Measured with
`just debug-codegen-eval-cost`, whose subject is `conformist codegen-repair --list`
(exactly the discovery eval, applying nothing).

Subject: conformist's own flake, `checks.x86_64-linux` with **225 attrs**, none of
which carries `codegenPatch` — so these are discovery costs alone, with no patch
build in them. Per-run wall clock, three runs per phase:

| Phase | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| clean | 4235ms | 2161ms | 1505ms |
| clean, no eval-cache | 1937ms | 1786ms | 1985ms |
| dirty | 4144ms | 1703ms | 1793ms |
| dirty, no eval-cache | 1792ms | 1582ms | 1694ms |

What this shows:

- **Discovery costs ~1.5–2.2s steady state** over 225 checks, with a one-off ~4.2s
  first run. The 4.2s appears in the first run of both the clean and the dirty
  phase but in neither no-eval-cache phase (which ran after something had already
  warmed), so it is a warm-up cost, not a dirtiness cost.
- **A dirty tree costs no more than a clean one** — 1.7–1.8s against 1.5–2.2s, which
  is within the run-to-run spread.
- **The eval cache does not apply.** Disabling it with `eval-cache = false` changed
  nothing measurable (1.8–2.0s against 1.5–2.2s); if the cache were serving this
  eval, removing it would have been visibly slower. The likely reason is that nix's
  eval cache keys on a locked flake fingerprint, which a local worktree ref does not
  have — but that explanation is inferred from the timings, not measured.
- **igloo#80 note 5 was not reproduced here.** That note theorised that a dirty
  tree forces a godyn graph build via IFD before the eval can answer; this run built
  **0 derivations** during a dirty-tree discovery. Caveat: the store was warm from
  earlier builds in the same session, so this observation is "nothing needed
  building at that moment", not "a dirty tree never triggers an IFD build". The
  cold-store variant is unmeasured. Any structural fix belongs to igloo#81.

**Discovery runs once per convergence pass, not once per run.** `converge`
re-discovers at the top of every iteration, so a run that applies _n_ patches pays
_n+1_ discoveries plus one patch build per outstanding check per pass. A repo with
three repairable checks that all need applying therefore pays about four
discoveries — on the order of 8s — not two. Re-discovering is deliberate (a patch
can in principle change the flake, and so the set of checks), but it means the
figures above are the floor, not the typical cost of a repairing run.

**Conclusion: the triggers stay.** Since the cost is per-invocation, scales with the
number of patches applied, and the eval cache offers no relief, relaxing the
triggers to "every commit" would add seconds to every commit in an adopting repo,
including commits that touch only prose. The trigger gate genuinely prevents that:
both the whole-tree repair path and the whole-tree check path skip a linter whose
matched set is empty (`format/repair.go`'s `RepairLinter` returns early on an empty
file list, and `format/check.go`'s `Finalize` `continue`s), so a commit staging no
`*.go`/`flake.lock`/`go.nix` file pays nothing. A full-tree `nix fmt` walks
everything, so it always pays at least one discovery.

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| trigger globs | `*.go`, `flake.lock`, `go.nix` | What actually moves a generated file: a changed type or config struct, and the two lockfiles through which a producer's new output reaches a consumer. Kept narrow because discovery costs ~2s per run and is not cached. | A repo finds drift landing that the triggers missed, or the measured discovery cost drops far enough that "every commit" becomes affordable |
| convergence pass limit | `max(4, 2*checks+2)` | One apply per check would do for independent checks; the doubling allows a legitimate chain, where one generator's output is another's input. Beyond that is a cycle, not a chain. | A real repo hits the limit with generators that are chained rather than cyclic |
| `strict` default | `false` | The lane runs in a pre-commit hook, where exiting non-zero blocks the commit; an unreachable nix must not block a whole repo when the drift check still catches the staleness. Note the option is currently inert through the linter wiring — see Limitations. | Repos routinely discover staleness the lane silently failed to repair |
| nix stderr kept on failure | last 2000 bytes | nix puts the actual cause last, and a commit hook's output has to stay readable. | Diagnoses routinely need context the tail cuts off |

## More Information

- conformist#124 — the feature, and the nine design decisions this implements
- igloo#80 — the `codegenCheck` / `passthru.codegenPatch` contract consumed here,
  including the module-root (note 2), overlapping-checks (note 3), IFD-cost (note 5)
  and git-fidelity (note 6) notes
- igloo#81 — where a structural fix for discovery-time IFD would go
- tommy#144, spinclass#322 — the producer and hook sides of the same rollout
- conformist#55 / #56 / #57 — the restage / stage-new / stage-deleted tiers this
  lane opts into, and the per-linter attribution that makes that safe
- conformist#47 — pre-commit hooks as store-path commands
- `docs/rfcs/0005-conformist-profile-and-cache-delivered-tools.md` — the profile
  this lane is expected to move into at POC v2
- `conformist.toml(5)` — the linter option surface
