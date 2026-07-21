conformist-justfile(7)

# NAME

conformist-justfile - justfile conventions: the doctrine and its enforcement

# DESCRIPTION

Justfiles in eng-fleet projects follow a consistent set of design patterns for
naming, hierarchy, and composition. This page is the *single normative home*
for those conventions — both the prose rationale and the precise, enforceable
form conformist's whole-tree *justfile-\** linters apply. It absorbs and
supersedes *eng-design_patterns-justfile*(7), which is now a pointer here
(eng#189).

Where a convention has legitimate exceptions, this page pins the exact
exemptions so the rule is a hard, checkable MUST. Each enforced rule names the
linter that enforces it; the linters read recipe metadata from
_just --dump --dump-format json_. Sections without a named linter are
convention only — deliberately unenforced.

# NAMING CONVENTION

Every recipe follows a *verb-noun* pattern where the verb is the action
category and the noun is the tool or domain.

Examples: *build-go*, *test-bats*, *codemod-fmt-go*, *update-nix*.

Verbs nest for sub-categories: *codemod-fmt-go* is *codemod* (verb) + *fmt*
(sub-verb) + *go* (noun).

# VERB LIST

The verb is the first _-_-delimited segment and MUST be one of the canonical
verbs:

	build test validate verify lint run list codemod
	install deploy load bump update clean debug explore

The version-release recipes *tag* and *release* and the entrypoint *default*
are exempt from verb-noun. Enforced by *justfile-recipe-names*. This list is
the single source of truth; the linter's hardcoded copy mirrors it — keep the
two in sync. VERB CATEGORIES below gives each verb's meaning.

# VERB CATEGORIES

The verbs below are ordered roughly by lifecycle position
(pre-build → build → post-build → operational).

The *validate* / *verify* / *lint* / *test* split follows the Apache
Maven lifecycle vocabulary, which itself reflects the older IEEE 1012
verification-and-validation distinction:

- *validate* answers "are the inputs / specification correct?" --- runs
  before build, fails on hard parse / schema errors.
- *verify* answers "does the built output meet its acceptance criteria?"
  --- runs after build, smoke-tests or Gate-style checks on the
  artifact.
- *lint* checks for stylistic or idiomatic issues without modifying
  code. Lints are opinions; validation failures are hard errors.
- *test* runs an actual test suite.

This makes the verb tell you *when* the recipe runs and *what kind of
failure* it produces, not just that it "checks something". See SEE
ALSO for the underlying references.

*build*
	Compile, generate, or produce artifacts.
	Examples: _build-go_, _build-gomod2nix_, _build-nix_, _build-moxins_.

*test*
	Run test suites.
	Examples: _test-go_, _test-bats_, _test-cargo_.

*validate*
	Pre-build / static check that inputs and specifications are
	well-formed. Hard failures (parser, schema, manifest, type-eval).
	Equivalent to Maven's _validate_ phase.
	Examples: _validate-manpages_ (scdoc syntax), _validate-devshell_,
	_validate-formatting_ (conformist check). _nix flake check_ is a
	*validate* only when no flake output forces an
	import-from-derivation; on IFD flakes it becomes a post-build
	*verify* (see VERIFY-NIX (IFD FLAKES)).

*verify*
	Post-build / smoke-style check that the produced artifact meets
	acceptance criteria. Runs against the installed thing, not the
	source. Equivalent to Maven's _verify_ phase.
	Examples: _verify-podman-darwin_ (Gate-1 smoke of the installed
	launchd agent + running VM), _verify-nix_ (flake-schema check on
	a flake whose evaluation forces a build — see VERIFY-NIX (IFD
	FLAKES)).

*lint*
	Style / convention / idiom check that does not modify code.
	Opinion-driven, often configurable severity. Distinct from
	*validate* (which is parser-strict) and *codemod* (which modifies
	code).
	Examples: _lint-fmt_ (the read-only conformist gate via
	_checks.formatting_), _lint-worktree_ (impure git-state linters),
	_lint-rust_ (_clippy_), _lint-go_ (_golangci-lint_),
	_lint-manpages-see-also_ (SEE ALSO backref + symmetry checks).

*run*
	Execute the built artifact.
	Examples: _run-nix \*ARGS_.

*list*
	Query / inspect state. Read-only, prints a result, makes no
	changes. Use this for "what's running / what's installed / what's
	pending" style recipes.
	Examples: _list-active_sessions_ (open agent sessions and
	worktrees that would break on environment upgrade).

*codemod*
	Automated code modifications. Sub-verbs:

	*fmt* — format code (the conformist repair lane / _nix fmt_;
	individual tools: _gofumpt_, _nixfmt_, _shfmt_, _rustfmt_).

	*fix* — auto-fix issues (_go fix_, _eslint --fix_).

	(*lint* is intentionally NOT a sub-verb here --- linting does not
	modify code and lives as its own top-level *lint* verb above.)

*install*
	Deploy a built artifact onto the current system. Side-effectful;
	touches user environment, launchd / systemd, _~/.local_, _/etc_,
	etc.
	Examples: _install-niri-session_.

*deploy*
	Push a local artifact to a remote system or registry. Distinct
	from *install* in that the target is not the local machine.
	Examples: _deploy-flake-lock_ (push flake.lock to origin).

*load*
	Start, activate, or load a service / agent / runtime resource that
	is already installed. Inverse of stop (no formal verb yet).
	Examples: _load-agents_, _load-tent-image_.

*bump*
	Update a pinned version reference to a specific new value.
	Distinct from *update* in that bump pins to an explicit target,
	while update follows whatever the lock or branch resolves to.
	Examples: _bump-nixpkgs_, _bump-version_.

*update*
	Refresh dependencies or inputs. Follows whatever the lock /
	branch / remote resolves to.
	Examples: _update-go_, _update-nix_, _update-git_.

*clean*
	Remove generated artifacts.
	Examples: _clean-build_, _clean-result-symlinks_.

*debug*
	Diagnostic / inspection recipes. Often paired with the
	_[group("debug")]_ attribute. Throwaway investigations live here
	and may be deleted once their question is answered (each carries a
	comment naming the issue it serves).
	Examples: _debug-nix-duplicates_, _debug-podman-darwin-\*_,
	_debug-top-procs_.

*explore*
	One-off recipes for exploring a new tool, API, or behavior.
	Lifetime is typically a single session or feature; promoted to
	*debug-\** if they outlive their explore.

# AGGREGATES AND LEAVES

A recipe is an *aggregate* when it has no body and only lists dependencies,
and a *leaf* when it has a body that does the actual work. Bare-verb recipes
are aggregate targets that compose specific verb-noun leaves as dependencies:

```
build: build-gomod2nix build-go
test: test-go test-bats
codemod-fmt: codemod-fmt-go codemod-fmt-nix
```

This gives two usage modes: *just build* runs everything, *just build-go*
runs one step.

Enforced rules:

- *default* MUST be the first recipe and MUST list only aggregates. Enforced
  by *justfile-default*. (See DEFAULT RECIPE.)
- A *leaf* MUST have a noun: its name MUST NOT be a bare verb (_test-go_, not
  _test_). A bare verb names an aggregate. Enforced by *justfile-leaf-noun*
  (see ANTI-PATTERNS, "redundant nouns").
- An *aggregate* MUST NOT carry a doc comment — its dependency list is
  self-documenting. Enforced by *justfile-aggregate-comments* (see
  ANTI-PATTERNS, "comments on aggregates").

# TASK HIERARCHY

The ideal shape is "every leaf belongs to exactly one aggregate." That is a
SHOULD: standalone operational and maintenance recipes legitimately belong to
no aggregate. The enforceable form splits on the leaf's verb:

- *Pipeline-verb leaves* — verb _build_, _test_, _validate_, _verify_, _lint_,
  or _codemod_ — MUST belong to *exactly one* aggregate. These are the recipes
  the build-and-verify pipeline composes: an un-aggregated one is unreachable
  from _default_, and one in two aggregates is duplicated work.
- *Other leaves* — verb _run_, _list_, _install_, _deploy_, _load_, _bump_,
  _update_, _clean_, _debug_, _explore_, plus _tag_ and _release_ — MAY be
  orphans (standalone), but MUST NOT belong to more than one aggregate.
- *Private* recipes (underscore-prefixed or _[private]_) are exempt entirely.

Enforced by *justfile-task-hierarchy*.

# DEFAULT RECIPE

```
default: validate build test
```

*default* chains aggregate targets into a single "build and verify" pipeline.
If *just* (no arguments) passes, the project is in a good state. This is the
CI-equivalent target.

*default* contains only aggregate targets --- never leaves directly. A
standalone leaf like *validate-devshell* lives under its lifecycle
aggregate (*validate*), and the aggregate goes in *default*. This keeps
the aggregates-only rule (see TASK HIERARCHY) intact at the entrypoint.

The *default* recipe is always the first recipe in the file. Enforced by
*justfile-default*.

# MODULES

A _mod_-imported child justfile's recipes are addressed module-qualified
(_explore::debug-widget_, nested _a::b::c_). The naming rules apply to the
recipe's *own* name: linters strip every leading module qualifier before verb
extraction (conformist#85). *justfile-recipe-names* checks
every module recipe; *justfile-task-hierarchy* checks each module scope with
the same per-verb bounds, crediting a root aggregate that lists _mod::leaf_ —
ownership is matched on bare recipe names, because _just --dump_ drops the
module qualifier from dependency entries, and a bare name duplicated across
scopes is skipped as ambiguous.

# RECIPE DESCRIPTIONS

Every *leaf* recipe MUST carry a doc comment: the single line immediately
above it (or an explicit _[doc("...")]_), which _just --list_ shows as the
description. Aggregates are exempt (see AGGREGATES AND LEAVES). debug/explore
leaves are checked with a throwaway-specific message by
*justfile-debug-recipes*; all other leaves by *justfile-recipe-descriptions*.

*just --list* derives each recipe's description from the *single comment line
immediately preceding the recipe* --- the line just above its attributes. A
multi-line comment block therefore surfaces only its *last* line, which reads
as a truncated fragment:

```
# Rewrite version.env (canonical source of truth) and the matching
# Cargo.toml `version` line. Pure mutation; scoped to [package] so it
# never touches [dependencies.*] version lines,
# which also sit at column 0.
[group("maintenance")]
bump-version new_version:
    ...
```

renders as:

```
bump-version new_version  # which also sit at column 0.
```

That is the tail of the block, not a usable summary. To keep
descriptions readable:

- Keep the comment line *immediately above* the recipe a concise
  one-line summary --- this is what *--list* shows.
- Move longer rationale *into the recipe body* as inline comments.
- Or set an explicit description with *[doc("...")]*, which is
  independent of the leading comment.

# LIFECYCLE GROUPS

Recipes are grouped by the *lifecycle stage* they belong to, not by
their verb prefix. The verb tells you *what kind of action* a recipe
is; the group tells you *when in the lifecycle* it runs. These two
axes are independent: a *validate-\** recipe and a *lint-\** recipe
are different verbs but share the *pre-build* group, while
*build-home* and *build-rcm* share both the verb and the group, and
*verify-podman-darwin* shares the group with *test-integration*
despite having a different verb.

A recipe's group is *derived from its verb* by the mapping below. An explicit
_[group("...")]_ attribute is therefore only needed when the group differs
from the verb-implied stage — a cross-axis recipe such as a _verify-\*_ that
actually runs pre-build — or to mark _debug_ / _explore_ throwaways. There is
deliberately no linter requiring an explicit group on every recipe; this
section records the convention so the absence of enforcement is not an
oversight.

Canonical groups (in lifecycle order):

*pre-build*
	Static checks that run before *build*. Failure here means the
	source / config is malformed; nothing downstream can succeed.
	Members: *validate-\**, *lint-\**.

*build*
	Compile / generate / produce artifacts. Members: *build-\**.

*post-build*
	Smoke, acceptance, or test-suite checks against built artifacts.
	Members: *verify-\**, *test-\**.

*operational*
	Side-effects on the running system: installs, deploys, loads.
	Members: *install-\**, *deploy-\**, *load-\**.

*maintenance*
	Keep the environment fresh or tidy. Members: *update-\**,
	*bump-\**, *clean-\**.

*codemod*
	Modify source code. Members: *codemod-fmt*, *codemod-fix*.

*inspection*
	Read-only state queries; print and exit. Members: *list-\**.

*debug*
	Diagnostic / throwaway recipes. Often paired with one-off issue
	references in their comments. Members: *debug-\**.

*explore*
	Discovery / one-off experiments. Promoted to *debug-\** if they
	outlive their initial question. Members: *explore-\**.

*Cross-axis examples* (group is not just the prefix):

- *verify-podman-darwin* --- verb is _verify_, group is *post-build*
  (it runs against the installed launchd agent, not against source).
- *load-tent-image* --- verb is _load_, group is *operational* (it
  loads a container image into the local registry; it doesn't build
  one).
- *validate-manpages* --- verb is _validate_, group is *pre-build*
  (scdoc compile is a parser pre-flight, not a runtime check).
- *bump-nixpkgs* --- verb is _bump_, group is *maintenance* (the
  bump is environment-refresh, not part of the build).
- *verify-nix* --- verb is _verify_, group is *post-build* on an IFD
  flake (the _nix flake check_ forces the build, so it runs against the
  built artifact). It would be *validate* / *pre-build* on a non-IFD
  flake. See VERIFY-NIX (IFD FLAKES).

*Aggregate recipes* take the group of their dominant phase. *build*
(= *build-nix build-rcm build-home load-agents*) is *build*; *clean*
is *maintenance*; *update* is *maintenance*. *default* is left
ungrouped --- it's the entrypoint, above the lifecycle.

*Private helpers* (prefixed with underscore and / or marked
*[private]*) are typically ungrouped; they don't show up in
*just --list* anyway.

# VALIDATE-DEVSHELL

Repos that ship a Nix devShell should include a *validate-devshell* recipe
under the *validate* aggregate:

```
# Verify the devShell evaluates and builds without errors. Catches
# vendor-env / goFlakeInputs / mkGoEnv breakage that the prod-binary
# build can mask. No store-output usage --- just a build-check.
validate-devshell:
    nix build --no-link .#devShells.{{ arch() }}-linux.default

validate: validate-devshell

default: validate build test
```

The verb is *validate* (not *build*) because the recipe is a pre-build
parser-and-eval check with no kept artifact (*--no-link*). It fits the
*validate* category in VERB CATEGORIES: hard failure on parse / schema /
type-eval against a Nix input.

*Why this is its own recipe.* _nix build .#default_ (the prod-binary build)
and _nix build .#devShells.<system>.default_ (what direnv evaluates on
shell entry) are different derivations built through different code paths
(different vendor-env, _mkGoEnv_ vs _buildGoApplication_, etc.). The prod
build can succeed from cache while the devShell build surfaces a
vendor-env or _goFlakeInputs_ regression. Without *validate-devshell* in
*default*, the first thing to catch the breakage is a _direnv reload_ or a
fresh worktree --- a bad place to discover the devshell is broken.

*Conventions.*

- *--no-link* is intentional: this is a build-check, not a usable
  artifact. The prod *build-nix* recipe is the one that emits a usable
  out-link.
- *{{ arch() }}* lets the same recipe work on any host architecture.
- Repos without a devShell skip this recipe entirely; it's only
  relevant when the flake exposes _devShells.<system>.default_.
- Default to checking only _devShells.default_. Repos with multi-devshell
  setups (e.g. separate _go_, _bats_, _rust_ devshells) extend with
  additional leaf recipes as needed.

# LINT-FMT

Repos that drive formatting through conformist's module (the standard shape —
_nix fmt_ wired to the module eval's wrapper) pair a *lint-fmt* read-only gate
with the modifying *codemod-fmt* recipe:

```
# Read-only formatting gate: builds the `checks.formatting`
# derivation, which runs conformist check against a /nix/store
# snapshot of the source tree and fails if anything would change.
# Does NOT modify files in the worktree --- the modifying
# counterpart is `codemod-fmt`.
lint-fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.formatting" --no-link --print-build-logs

lint: lint-fmt

default: validate lint build test
```

The *lint-fmt* / *codemod-fmt* pair shares one conformist config:
one recipe rewrites the worktree (_nix fmt_, which is sugar for
_nix run .#formatter.${system}_), the other fails-loudly when
formatting would change anything (the *checks.formatting* derivation
the module's _build.check_ exposes).

*Why a separate recipe.* _nix fmt_ at the CLI rewrites files in place,
which is exactly what you don't want in a CI gate or merge hook. The
*checks.formatting* derivation runs the same config against a
sandboxed /nix/store snapshot --- if formatting would change anything,
the build fails. Threading that through *lint-fmt* gives the gate
without the rewrite.

*Conventions.*

- *--no-link* / *--print-build-logs* match the convention for
  read-only nix-build gates: no kept artifact, full log on failure.
- *lint-fmt* belongs to the *lint* aggregate (and the *pre-build*
  group); *codemod-fmt* belongs to the *codemod* group. They share a
  config, not an aggregate.
- Impure git-state linters (the eng-impure preset) run via a separate
  *lint-worktree* leaf in the *lint* aggregate — they need a live
  _.git_, which the sandbox lacks. See *conformist-nix*(7).
- Repos without the module wired up can still expose a *lint-fmt*
  recipe that calls per-tool checkers in *--check* mode (e.g.
  _gofumpt -l_, _shfmt -d_), but the single-derivation pattern is
  preferred because it stays sandbox-pure.

# VERIFY-NIX (IFD FLAKES)

_nix flake check_ is the canonical *validate* example — a cheap
pre-build static gate. But that only holds for flakes whose outputs
evaluate without forcing a build. When a flake output triggers
*import-from-derivation* (IFD), the check stops being a pre-build gate
and belongs in post-build *verify* instead.

*The IFD case.* gomod2nix's _goFlakeInputs_ / _flake-input-go\_mod_
bridge builds a _merged-go.mod_ derivation and reads it back at eval
time (IFD). Evaluating _apps.default.program_ forces the package
derivation, which transitively must realize that IFD. Under
_--no-build_ the check cannot build it and aborts:

```
error: path '...-merged-go.mod.drv' is not valid
```

Requiring the built artifact is the defining property of *verify*, not
*validate*. So on an IFD flake the recipe is *verify-nix*, sits in the
*post-build* group, and takes a *build-\** prerequisite to warm the IFD
before the check runs.

*Eval-cache poisoning.* IFD breaks eval purity, so the flake eval cache
keys a failed _--no-build_ run on the flake fingerprint, not on store
state. After the IFD is later built, a cached run aborts with:

```
error: evaluation of cached failed attribute '...' unexpectedly succeeded
```

_--no-eval-cache_ makes the gate deterministic regardless of cache
history.

*Reference recipe* (_chrest_):

```
[group("post-build")]
verify-nix: build-nix
    nix flake check --no-build --no-eval-cache
```

Removing the upstream IFD would let the check move back to a pre-build
*validate*.

# DEPENDENCY ORDERING

*Positional dependencies* (prerequisites — run before body):

```
build-go: build-gomod2nix
    nix develop --command go build -o build/myapp ./cmd/myapp
```

*&& dependencies* (post-steps — run after body):

```
update-go: && build-gomod2nix
    nix develop --command go mod tidy
```

Use positional for prerequisites. Use *&&* for "then regenerate/rebuild after
the main action completes."

# RECIPE ATTRIBUTES

*[group("name")]*
	Organize related recipes for *just --list* output. Groups capture
	*lifecycle stage*, not verb prefix --- so a verify-\* recipe lives in
	post-build, not in its own "verify" group. See LIFECYCLE GROUPS
	for the canonical taxonomy.

```
[group("post-build")]
test-go:
    go test ./...
```

*[private]*
	Hide from *just --list*. Use for internal helper recipes. Private helpers
	are also conventionally prefixed with underscore:

```
[private]
_test-bats-ensure-fixtures:
    ...
```

*[macos]* / *[linux]*
	Platform-conditional recipes. Only runs on the matching OS:

```
[macos]
load-agents:
    launchctl bootstrap gui/$UID ~/Library/LaunchAgents/...

[linux]
load-agents:
    @true
```

*[working-directory: 'path']*
	Change the working directory for a recipe:

```
[working-directory: 'rcm']
build-rcm:
    rcup -v
```

*[no-exit-message]*
	Suppress the exit message on failure.

*[doc("description")]*
	Set an explicit *just --list* description, independent of the leading
	comment. Use when a recipe's rationale needs more than one comment
	line (see RECIPE DESCRIPTIONS).

# VARIABLES

Common variable patterns:

```
dir_build := "build"
cmd_nix_dev := "nix develop " + justfile_directory() + " --command "
```

Use *justfile_directory()* for paths relative to the justfile. Use *export*
to inject variables into recipe environments:

```
export MOXIN_PATH := justfile_directory() / "build" / "moxins"
```

# PARAMETERIZED RECIPES

Recipes accept parameters with optional defaults:

```
run-nix *ARGS:
    nix run . -- {{ARGS}}

man-search query top_k="10":
    bin/man-search-embed.bash "{{query}}" "{{top_k}}"
```

Use *\*args* for variadic pass-through. Use *name=default* for optional
parameters.

# SHEBANG RECIPES

Use shebang recipes for multi-line logic that needs conditionals or loops:

```
deploy-flake-lock:
    #!/usr/bin/env bash
    set -euo pipefail
    if git diff --quiet flake.lock; then
      echo "flake.lock unchanged, skipping deploy"
      exit 0
    fi
    git add flake.lock
    git commit -m "chore: update flake.lock"
```

Always use *set -euo pipefail* in bash shebang recipes.

# CROSS-JUSTFILE DELEGATION

Subprojects may have their own justfiles. Delegate to them using *just*
recursion:

```
test-bats-targets *targets: build
    just zz-tests_bats/test-targets {{targets}}
```

# ANTI-PATTERNS

- *Mixed verb categories* — don't combine verbs like _build-go-test_.
  Use separate recipes.
- *Generic names* — don't use _all_, _dev_, _check_, _compile_. Use
  verb-noun.
- *Logic in aggregates* — aggregate recipes should only list
  dependencies, never have a body.
- *Missing default* — every justfile needs a _default_ recipe.
- *Comments on aggregates* — the dependency list is self-documenting
  (enforced by *justfile-aggregate-comments*).
- *Redundant nouns* — even with one tool, use _test-go_ not _test_.
  You may add more later (enforced by *justfile-leaf-noun*).
- *deps as a verb* — use _update-\*_ for dependency management.

# EXAMPLE

```
default: validate lint build test

validate: validate-devshell

validate-devshell:
    nix build --no-link .#devShells.{{ arch() }}-linux.default

lint: lint-fmt

lint-fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.formatting" --no-link --print-build-logs

build: build-gomod2nix build-go build-nix

build-go: build-gomod2nix
    nix develop --command go build -o build/myapp ./cmd/myapp

build-gomod2nix:
    nix develop --command gomod2nix

build-nix:
    nix build --show-trace

run-nix *ARGS:
    nix run . -- {{ARGS}}

test: test-go test-bats

test-go:
    nix develop --command go test ./...

test-bats:
    nix develop --command bats --tap tests/

codemod-fmt: codemod-fmt-conformist

codemod-fmt-conformist:
    nix fmt

update-go: && build-gomod2nix
    nix develop --command go mod tidy

clean: clean-build

clean-build:
    rm -rf result build/
```

# SEE ALSO

*conformist*(7), *conformist-nix*(7), *conformist.toml*(5), *eng*(7),
*eng-versioning*(7)

The verb categories track the Apache Maven lifecycle vocabulary; see
https://maven.apache.org/guides/introduction/introduction-to-the-lifecycle.html
for the canonical reference. The underlying verification-and-validation
distinction comes from IEEE 1012.

Just's recipe-attribute reference (including *[group("...")]*,
*[private]*, *[macos]* / *[linux]*, *[working-directory: "..."]*, and
*[no-exit-message]*) lives at
https://just.systems/man/en/attributes.html.
