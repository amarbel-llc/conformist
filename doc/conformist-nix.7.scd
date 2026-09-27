conformist-nix(7)

# NAME

conformist-nix - nix flake conventions conformist enforces

# DESCRIPTION

conformist self-enforces a small set of Nix flake conventions through its
whole-tree linters — the *flake-\** rules in its Nix module. This page is the
normative home for those conventions: each section states a rule and names the
linter that checks it.

The rules are deliberately narrow. They cover flake *hygiene* that a static
check can verify from committed files alone (_flake.nix_, _flake.lock_), not Nix
style, evaluation, or the authoring of justfile recipes that invoke nix.

See *conformist*(7) for conformist's concepts and *conformist.toml*(5) for the
configuration format.

# FLAKE HYGIENE

*flake.lock* MUST be committed.

A flake's _flake.lock_ pins every input to an exact revision. Committing it is
what makes a checkout reproducible: a fresh clone resolves the same input
closure the author built against, and a downstream consumer that pulls the flake
gets deterministic inputs rather than whatever a registry resolves that day. An
uncommitted lock silently re-resolves inputs per machine and per day.

This is distinct from *trusting* the lock to prove a flake works. A committed
lock only resolves _inputs_; it does not evaluate _outputs_. Verifying that a
flake actually evaluates is a separate concern (*nix flake check*), not
something the lock provides — do not treat a present lock as evidence the flake
builds.

Enforced by the *flake-lock* linter, which fails when _flake.nix_ is present at
the tree root but _flake.lock_ is not.

# FLAKE OUTPUTS

A flake's *outputs* formal MUST name every declared input, or end with a _..._
catch-all.

Nix passes every declared input as an attribute to the _outputs_ function. A
closed formal — _{ self, nixpkgs }:_ without the ellipsis — that omits an input
is a hard evaluation error the moment that input is added, and it surfaces only
at eval time. Naming every input, or ending the formal with _..._, keeps the
flake evaluable as its inputs grow.

Enforced by the *flake-outputs* linter (conformist#9), which reads the declared
inputs from _flake.lock_ and fails when the _outputs_ formal is a closed set
that omits one.

# GO MODULE LOCK

A repo that builds with *gomod2nix* MUST keep _gomod2nix.toml_ in sync with
_go.mod_/_go.sum_.

_gomod2nix.toml_ is generated from the Go module graph by the *gomod2nix* CLI
and pins each dependency's version and source hash for the Nix build. When
_go.mod_/_go.sum_ change but _gomod2nix.toml_ is not regenerated, the Nix build
sees a stale dependency set — at best a build failure, at worst a silently
wrong closure.

Enforced by the *gomod2nix* linter. Its read-only *command* regenerates the
lock into a scratch directory (*gomod2nix --outdir*) and diffs it against the
committed _gomod2nix.toml_, failing on any difference; its *repair-command*
regenerates _gomod2nix.toml_ in place and stages it. Because regeneration needs
the real module graph (network / module cache) and the repair stages via
_git_, the linter runs in the impure working-tree lane, not the sandboxed
formatting check. It watches _go.mod_/_go.sum_ directly: those files are in
conformist's default *excludes* (so no formatter rewrites them), but as a
whole-tree check (_passes-files = false_; see *conformist.toml*(5)) it is exempt
from the global *excludes* --- its *includes* are a trigger gate, not an input
set --- so it still fires when _go.mod_/_go.sum_ change.

The *gomod2nix* CLI has no native non-writing check mode; the regenerate-and-
diff check is a workaround pending amarbel-llc/gomod2nix#14.

# CODEGEN DRIFT

A flake check that detects stale generated output SHOULD also say how to fix it,
by carrying a *passthru.codegenPatch* derivation whose _$out/patch_ brings the
tree back in line (empty when it is already current).

Such a check is otherwise only half a mechanism: it tells an author that a
generated file is stale, at the merge gate, and leaves them to discover this
repo's particular regenerate incantation and amend. Since every generator
differs --- a config header, a facade export, a lockfile's downstream stamp ---
there is no one command to run, but there IS one thing they share: the check
that already detects the drift.

Consumed by the *codegen-repair* linter, which walks _checks.<system>_,
builds every attr carrying that passthru, and applies each patch to the working
tree. It is enabled by *conformist.lib.presets.eng*, so an eng repo has the lane
without configuring anything, and a repo whose checks carry no
*passthru.codegenPatch* gets a no-op. Nothing in it names a generator.

The linter is *repair-only*: its read-only *command* is a deliberate no-op,
because the drift check carrying the passthru is already the check-mode gate.
Running a second copy of it would double-report every finding, pay for a second
build, and leave two places to debug. That is also why the lane is safe in the
PURE *eng* preset despite needing live _nix_ and _git_ --- check mode never
invokes a repair.

The patch is applied with *git apply -p2*, rooted at the tree root, or at
*passthru.codegenPrefix* when the check declares one (its module root, which may
be a repo subdirectory). A patch from a subdirectory-rooted module whose check
declares NO prefix is refused rather than applied at the wrong prefix.

Discovery and per-check build failures WARN and exit 0, because the lane runs in
a git pre-commit hook where a non-zero exit blocks the commit: an unreachable
_nix_ must not stop a repo from committing, and the drift check still catches
whatever staleness results. A patch that was built but will not APPLY exits 2 ---
it was generated from this very tree, so a refusal is a real inconsistency. The
*strict* option promotes the warnings.

Those signals currently reach a human only when *conformist codegen-repair* is
run DIRECTLY. conformist discards a _repair-command_'s non-zero exit and logs its
captured output at debug level, so through the linter wiring the exit 2 does not
fail the run, the warnings need *-vv* to appear, and *strict* is inert. The drift
check therefore remains the signal an operator actually sees. Making them
effective is a conformist-wide decision about how repair-command exits and output
are surfaced for every linter.

Because a *codegenPatch* rewrites, creates and deletes generated files, the
linter opts into all three staging tiers (*restage-repair-outputs*,
*stage-new-outputs*, *stage-deleted-outputs*; see *conformist.toml*(5)) so those
outputs land in the commit that triggered the repair.

See *conformist*(1) *codegen-repair* for the subcommand, and
_docs/features/0001-generic-codegen-repair-linter.md_ for the design, its
deliberate limitations, and the measured cost of discovery.

# SEE ALSO

*conformist*(7), *conformist.toml*(5)
