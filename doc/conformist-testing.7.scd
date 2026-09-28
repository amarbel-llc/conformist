conformist-testing(7)

# NAME

conformist-testing - how conformist is built, tested and gated

# DESCRIPTION

This page is the contributor-facing reference for conformist's own build and test
machinery: which lane runs what, what each sandbox can and cannot do, and the traps
that have actually bitten. It documents conformist's development, not its behaviour
as a tool --- for that see *conformist*(7).

Recipe names and their one-line descriptions are not repeated here. *just --list*
has them, and the just-us clown plugin hoists them into an agent's system prompt.
What follows is the rationale a doc comment has no room for.

# NO AMBIENT GO

Dependencies live in _go.nix_ (igloo FDR 0008). There is no _go.mod_, no _go.sum_
and no _gomod2nix.toml_ in the checkout, and no *go* in the devShell. Go commands run
inside nix through *godyn-go*; see *godyn*(7).

*just update-go* and *just update-go-get* go through *godyn-go*, which needs the
*impure-derivations* experimental feature, and the result is ingested back into
_go.nix_.

# BUILD BACKENDS

*packages.{default,conformist}* is the *godyn* build (igloo FDR 0007/0008):
*buildGoAuto* over _go.nix_ with no *strategy*, producing the *conformist* and
*flakeclobber* binaries with git burned in through *git.Binary* ldflags, joined with
*manpages*.

The package graph is derived at eval time --- nothing committed, per-system, so it
cannot drift. The cost is *ca-derivations* on every building host, and evaluating
another system's packages needs a builder for that system (igloo#75). Consumers
inherit both properties, and a consumer bumping conformist also needs a new enough
igloo, because of the *follows*.

*packages.conformist-bga* is *passthru.bga*, the input-addressed escape hatch and the
other side of the backend benchmark.

*packages.conformist-static* is the portable static x86_64 release binary that the
*release-assets* post-merge target uploads. It is bga with *CGO_ENABLED=0*, netgo and
osusergo, *-s -w*, the nixpkgs Go patches' tzdata/mailcap/iana-etc paths stripped, and
*allowedReferences = [ ]*. It uses bga rather than godyn because godyn's cgo-on stdlib
cannot link statically yet. The aarch64 cross link fails, so x86_64 is the shipped
artifact.

# TEST LANES

*just test* runs godyn's per-package test lane, _checks.<system>.conformist-tests_.
It is a *tags = [ "test" ]* instance, because dewey's *test_ui* package sits behind
that build tag and _test/test.go_ imports it.

The lane is sandboxed, with formatters, git, jj and bash as *nativeCheckInputs*, and
*testFiles* for fixtures a package reads from outside its own directory.

*just debug-test-pkg PKG RUN* runs one package through *godyn-test* --- the tight
loop while iterating. A NEW file must be *git add -N*'d first or it is invisible to
the build.

Two rules exist because of bugs:

*Test stub scripts resolve bash by absolute path*
	Never _/usr/bin/env_. A stub script must find its interpreter without relying
	on PATH, which some lanes deliberately empty.

*The cmd package's TestMain pins the ceiling directories*
	*GIT_CEILING_DIRECTORIES* and *CONFORMIST_CEILING_DIRECTORIES* are set to the
	temp root (conformist#15), so a test cannot walk up out of its fixture and
	discover the real repository. *test-go* additionally FAILS if the working tree
	mutates during the run, so a regression of that class cannot hide in a commit.

# LINT LANES

*just lint* is four lanes:

*lint-fmt*
	The sandboxed _checks.formatting_ gate: *conformist check* against a
	_/nix/store_ snapshot of the tree, failing if anything would change. Read-only
	by construction.

*lint-worktree*
	The impure git-state linters, which need a live _.git_ and so cannot run in the
	sandbox.

*lint-go*
	godyn vet plus godyn-lint: vet passes and staticcheck defaults, with *//nolint*
	honored. No golangci-lint.

*lint-go-analyzers*
	One godyn vet lane per dewey analyzer (conformist#10).

# WHAT THE SANDBOX CANNOT DO

_nix/linter-fixtures.nix_ runs each fixture in a pure *runCommandLocal* sandbox: no
network, and *no nix on PATH*. That single constraint decides where several tests
live.

The *conformist-flake-lock* merge driver needs a real *nix* to re-lock, so its
happy path cannot be gated there. *just explore-merge-driver-flake-lock* covers it
outside the CI lane instead, with a throwaway repo, a genuine _flake.lock_ conflict
and a real *git merge*; the sandboxed fixtures can only cover that driver's
fail-closed paths. The *conformist-codegen-header* driver needs no nix and IS gated
in *verify-linter-fixtures* --- including a driver-not-registered control, proving
the passing merge passes because of the driver rather than incidentally.

The Go test lane has the same constraint, which is why tests that drive
*conformist codegen-repair* stand in for nix rather than invoking it.

*just verify-linter-fixtures* builds the fixture aggregate cheaply. It is NOT
*nix flake check*, which would also realize the whole registry smoke-check set.

# DIAGNOSTIC RECIPES

The *debug-\** benchmarks and fleet-migration diagnostics are in no aggregate and
gate nothing.

*debug-bench-backends* emits per-build durations to stats-me as
*gobuild.conformist.<backend>.<phase>* timers --- a protocol shared with igloo's
dewey bench --- and uses *nixgc* for cold rebuilds.

*debug-codegen-eval-cost* times the codegen-repair discovery eval clean against
dirty and with the nix eval cache disabled. Its findings are recorded in
_docs/features/0001-generic-codegen-repair-linter.md_.

The *debug-flakeclobber-\** and *debug-flakeparse-\** recipes take the fleet root
from *$ENG_REPOS*, or an explicit *root=*. *debug-flakeclobber-regression* diffs a
reference commit's fleet-wide output against the working tree's, so a parser
widening can be SHOWN additive rather than assumed to be.

# THE MERGE GATE

*just* --- the default recipe, *validate build test verify lint* --- is the local CI
lane, and is exactly what spinclass's pre-merge hook runs. So do not run *just* or
*just lint* again immediately before merging; the hook will run them.

# SEE ALSO

*conformist*(7), *conformist-nix*(7), *conformist-conform*(7),
*conformist-justfile*(7), *eng-manpages*(7)
