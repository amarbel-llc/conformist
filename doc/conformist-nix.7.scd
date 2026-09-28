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

MODULE LIBRARY, at the end, is the other half of this page: the reference for the
module a consumer evaluates to get those linters in the first place.

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

The patch is applied with *git apply -p2*. *passthru.codegenPrefix* names the
module root it is relative to, and is tri-state: a subpath (applied with
*--directory=*), an explicit empty string (the check asserts its module is the
repo root), or _null_ --- unknown, which is the ordinary case for a root module,
since the prefix is auto-derived only for a *src = self + "/subdir"* module. With
a null prefix the tree root is the only candidate, so the patch is applied there
when its paths resolve and REFUSED otherwise, rather than written at a prefix
nobody confirmed.

Discovery and per-check build failures WARN and exit 0, because the lane runs in
a git pre-commit hook where a non-zero exit blocks the commit: an unreachable
_nix_ must not stop a repo from committing, and the drift check still catches
whatever staleness results. A patch that was built but will not APPLY exits 2 ---
it was generated from this very tree, so a refusal is a real inconsistency. The
*strict* option promotes the warnings.

A FAILURE blocks the commit or merge it gates, because the linter sets
*repair-must-succeed* (see *conformist.toml*(5)): conformist otherwise discards a
_repair-command_'s exit status, most repairs being best-effort. With it set, the
non-zero exit becomes an operational failure carrying the command's output, so the
run stops at exit 2 and reports why.

A soft failure still exits 0, and today does so silently: repair output is logged
at debug level, below the default, so a discovery failure is indistinguishable
from a clean no-op until the drift check catches it at the merge gate. Surfacing
it needs conformist to stop merging a tool's stdout and stderr, which is a
conformist-wide change rather than a property of this linter.

Because a *codegenPatch* rewrites, creates and deletes generated files, the
linter opts into all three staging tiers (*restage-repair-outputs*,
*stage-new-outputs*, *stage-deleted-outputs*; see *conformist.toml*(5)) so those
outputs land in the commit that triggered the repair.

See *conformist*(1) *codegen-repair* for the subcommand, and
_docs/features/0001-generic-codegen-repair-linter.md_ for the design, its
deliberate limitations, and the measured cost of discovery.

# MODULE LIBRARY

The rules above are enforced by linters shipped in conformist's Nix module. This
section is the reference for the module library itself --- what a consumer calls, and
what it gets back. *conformist*(7) NIX MODULE is the one-paragraph overview.

The library is *self-consumed*: conformist lints and formats its own tree with its
own module, with no treefmt-nix dependency (conformist#4). So the module cannot rot
separately from the tool.

## Entry points

_nix/default.nix_ is the pure library:

*evalModule*, *submoduleWith*, *mkConfigFile*, *mkWrapper*
	The core: evaluate a module, render a _conformist.toml_, wrap the binary.

*mkFormatterModule*
	Ported near-verbatim from treefmt-nix, so a _programs/<name>.nix_ module ports
	unchanged.

*mkLinterModule*
	Its linter analogue, emitting a *[linter.<name>]* stanza with optional
	*repair-command* / *repair-options*.

*writeCheckScript* (_nix/write-check-script.nix_)
	Packages a local script as a sandbox-safe linter command, via *patchShebangs*
	and a wrapper (conformist#19). A bare script in the tree does not survive the
	read-only _/nix/store_ check sandbox.

*wrapWithToolchain* (_nix/wrap-with-toolchain.nix_)
	One conformist wrapper carrying its formatter toolchain on PATH --- the
	non-module hermetic *nix fmt* / *--staged* hook for a repository with a
	hand-written _conformist.toml_ (conformist#51).

*mkToolchainHooks* (_nix/mk-toolchain-hooks.nix_)
	Returns three named wrappers, *{ formatter, preCommit, repair }*, installed as
	*conformist* / *conformist-pre-commit* / *conformist-repair*. It is the
	TOML-consumer mirror of the module's *build.{wrapper,preCommit,repair}*, so a
	hand-written-config repository wires its hooks exactly as a module adopter does
	(conformist#59). See *conformist*(7) MODES for how a hook uses them and why a
	bare binary skips file types whose formatter is off PATH.

*module-options.nix*
	Declares the settings surface and the
	*build.{devShell,configFile,wrapper,preCommit,repair,programs,check}* outputs.
	The per-tool submodule is *freeform*, so a field conformist's config struct
	understands but the module does not declare explicitly --- *repair-command*,
	*repair-must-succeed*, the staging tiers --- can be set without a module change.

## Registries

_nix/programs/_ with _programs.nix_ is the formatter registry; _nix/linters/_ with
_linters.nix_ is the linter registry. Both auto-discover: adding
_nix/linters/<name>.nix_ registers that linter.

Beyond general-purpose linters (shellcheck, ruff, statix, deadnix, typos, yamllint
and so on), the registry holds the *eng-convention enforcers* conformist runs on
itself. Each names the page that is normative for the rule it checks:

*eng-versioning*, *eng-versioning-deprecated-file*
	_version.env_ declares the canonical version, and neither a _version.txt_ nor a
	_flake.nix_ named version variable may reappear. See *eng-versioning*(7),
	"Deprecated alternatives".

*flake-outputs*, *flake-lock*
	FLAKE OUTPUTS and FLAKE HYGIENE above (conformist#9/#11).

*git-merge-drivers*, *git-remotes*, *git-default-branch*
	*conformist-git*(7) MERGE DRIVERS, REMOTES and DEFAULT BRANCH
	(conformist#68).

*gomod2nix*
	GO MODULE LOCK above.

*codegen-repair*
	CODEGEN DRIFT above (conformist#124).

*golangci-dewey*
	A repository gating on golangci-lint must wire the dewey plugin through
	_.custom-gcl.yml_ (conformist#10).

*agents-md*
	The CLAUDE.md-to-AGENTS.md migration, and the character cap on AGENTS.md
	itself. Check and repair.

*sweatfile*
	The spinclass sweatfile's shape.

*clippy*
	A first-class Rust lint (conformist#69): the check is
	*cargo clippy … -- -D warnings*, the repair is *cargo clippy --fix*.
	Whole-tree, with *restage-repair-outputs*.

	It is *impure* --- it compiles the crate --- so it runs in the working-tree
	lane only, never the sandboxed check. It is also *opt-in*: a registered module
	enabled with *linters.clippy.enable = true*, deliberately NOT in the eng-impure
	preset roster, so a non-Rust repository never pulls a Rust toolchain for a
	no-op.

	conformist pins NO Rust. The *packages* toolchain defaults to cargo, clippy,
	rustc and gcc from the consumer's own nixpkgs, and is overridden wholesale for
	a pinned toolchain from rust-overlay or fenix. Knobs: *manifest-path*,
	*workspace*, *all-targets*, *extra-args*, *deny*, *allow*.

	Its behavioural fixtures live in a separate *clippy-fixtures* aggregate, built
	by *just explore-clippy-fixture* and kept out of the verify lane so CI stays
	Rust-free.

## The justfile linters are not here

The seven *justfile-\** convention linters, and *justfile-orphan-summary*, ship from
just-us as *lib.conformistPresets.justfile*. They read *just --dump --dump-format
model*, a fork-only dump format a stock *just* rejects, and conformist must stay
strictly UPSTREAM of its consumers: just-us already takes conformist as an input, so
taking just-us here would be a cycle. The coupling therefore lives in the repository
that owns the parser.

The rules remain conformist's --- *conformist-justfile*(7) is still their normative
home --- but conformist does NOT currently run them on its own justfile. Reaching
them would need either the forbidden input or a source pin, and the fleet-adoption
route is still being designed, with papi as the likely injection point. So conformist
authors these conventions without being held to them: a known gap, not an oversight
(conformist#85/#89/#112). RFC 0005's *conformist.profile* is what closes it.

## Remarshal-free config generation

*mkTomlFormat* and *mkYamlFormat* replace *pkgs.formats.toml* and
*pkgs.formats.yaml*, whose *.generate* serializes through remarshal and so drags
*matplotlib* --- and through it *ffmpeg* --- into EVERY generated config as a
build-time dependency (conformist#60).

They keep *pkgs.formats.<fmt>.type* for value validation and swap *.generate* for a
*yj* json-to-toml/yaml step. They are passed to all modules via *defaultSpecialArgs*,
so every TOML and YAML config generator is remarshal-free: conformist's own config
plus the statix, stylua, taplo, yamllint and similar settings files.
*just verify-no-remarshal* guards against a new direct *pkgs.formats.{toml,yaml}* use
creeping back in.

## Presets

A consumer enables a whole convention roster with one import rather than enabling
each linter by hand. Exposed as *conformist.lib.presets.{eng,eng-go,eng-impure}*:

*eng*
	The pure lane: enforcers that read only committed files and so run inside the
	sandboxed *checks.formatting*. Enables *eng-versioning*,
	*eng-versioning-deprecated-file*, *flake-outputs*, *flake-lock*,
	*git-merge-drivers* and *codegen-repair*.

	It does NOT enable the *justfile-\** linters, despite their rules being
	conformist's --- see below.

*eng-go*
	The canonical Go formatter chain, *goimports* at priority 1 then *gofumpt* at
	priority 2 --- the sequence the fleet converged on. Formatters only, no
	linters. Kept separate so a non-Go repository importing *eng* never pulls a Go
	toolchain.

*eng-impure*
	The git-state lane, which needs a live _.git_ and so runs against the working
	tree rather than the sandbox. Enables *git-remotes*, *git-default-branch*,
	*sweatfile*, *agents-md* and *gomod2nix*.

	*clippy* is deliberately NOT here, even though it is impure: it would make
	every non-Rust repository build a Rust toolchain for a no-op.

conformist self-consumes all three (_nix/conformist.nix_ and
_nix/conformist-impure.nix_), so a preset cannot drift from what conformist itself
runs. A downstream consumer MUST set *conformist.package*: conformist is not in
nixpkgs, so the module's *package* option has no default.

# SEE ALSO

*conformist*(7), *conformist.toml*(5), *conformist-conform*(7),
*conformist-git*(7), *conformist-testing*(7)
