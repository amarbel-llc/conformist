conformist(7)

# NAME

conformist - concepts behind the linter and formatter multiplexer

# DESCRIPTION

*conformist* runs all of a project's formatters and linters with one command.
It walks the tree, matches each file to tools by glob, and runs the matched
tools in parallel — only on files that have changed since the last run.

conformist is a clean copy of *treefmt* v2.5.0 that adds first-class linting on
top of treefmt's formatter multiplexing. The formatter half is inherited
largely verbatim; the linter half, the *check* subcommand, and the repair and
check modes are the conformist additions, specified in RFC 0001. See
*conformist*(1) for the command-line interface and *conformist.toml*(5) for the
configuration format.

# FORMATTERS AND LINTERS

conformist distinguishes two kinds of tool.

*Formatter*
	Rewrites files in place to a canonical form. Declared under
	_[formatter.<name>]_. Matched files are batched by their formatter
	sequence and the batches run concurrently.

*Linter*
	Inspects files and reports problems without modifying them. Declared under
	_[linter.<name>]_. Its *command* is a read-only check that exits non-zero
	on a finding; an optional *repair-command* applies autofixes. A linter with
	no repair command does nothing in repair mode.

A formatter and a linter may share a name; they occupy separate namespaces.

# MODES

conformist has two modes of operation.

*Repair* (the default)
	Applies formatter rewrites and linter repair commands, modifying the tree.
	This is the bare *conformist* invocation. With *--commit*, conformist then
	commits exactly the files the run changed, with the message
	_chore: conformist fmt+fix_ (plus any *--trailer* lines), honoring the
	repository's commit-signing configuration. It exits 0 when the tree was already conformant, 3 when
	fixes were applied and committed, and 2 when it refuses to run — on an
	unclean working tree without *--allow-dirty*, or outside a git worktree.
	Refusal happens before any formatting. With *--amend*, conformist instead
	folds the run's changes into the existing HEAD commit via
	_git commit --amend --no-edit_ (keeping HEAD's message), producing one clean
	commit rather than a fixup; it additionally refuses when HEAD has no commit
	to amend or has already been pushed to a remote. With *--exit-zero-on-fix*,
	conformist exits 0 (not 3) when fixes were committed/amended — refusals and
	operational failures still exit nonzero — so a caller that treats any nonzero
	exit as failure (e.g. a pre-merge repair hook) sees a successful repair as
	success. Pre-merge hooks chain it
	with the check mode below: absorb mechanical fixes, then hard-fail on
	substantive findings.

*Staged* (lint-staged)
	With *--staged*, conformist formats only the files currently staged in the
	index and restages the formatted content; no commit is created, so the
	caller's own commit — message, signing, trailers — proceeds with
	conformant content. A fully staged file is formatted in the working tree
	and restaged. A partially staged file (staged with additional unstaged
	edits) has its staged blob formatted in isolation and restaged via the
	object store, leaving the working tree's unstaged hunks untouched. Exits 0
	when the staged content was already conformant, 3 when files were
	reformatted and restaged, and 2 when refused — outside a git worktree, in
	stdin mode, or under fail-on-change. With *--exit-zero-on-fix*, conformist exits 0 (not 3) on a
	successful restage — refusals and operational failures still exit nonzero —
	so the canonical git pre-commit hook command
	_conformist --staged --exit-zero-on-fix_ proceeds with the commit when the
	staged content was repaired in place.

	That command runs the *bare* _conformist_ binary, which resolves each
	formatter's _command_ from PATH. If the formatter toolchain (gofumpt,
	goimports, nixfmt, …) is not on the author's PATH at commit time, the staged
	repair *silently skips* those file types. Two supported ways to get a
	*toolchain-hermetic* hook:

	- *Nix module adopters:* use the _build.preCommit_ output (its formatter
	  commands are store paths, not bare names). The _#eng_ template wires it as
	  _conformist-pre-commit_; see *conformist-nix*(7).
	- *Hand-written conformist.toml:* wrap conformist with its toolchain on PATH
	  — _conformist.lib.wrapWithToolchain pkgs { conformist; tools; }_ builds a
	  wrapper whose PATH carries _tools_ — and point the hook at the wrapper,
	  e.g. _pre-commit = "conformist-fmt --staged --exit-zero-on-fix"_. See
	  *eng-design_patterns-conformist*(7) "THE CWD-AWARE WRAPPER".

*Check* (read-only)
	Runs every formatter and linter without writing to the tree, reporting what
	would change. This is the *conformist check* subcommand. It exits 0 when
	clean, 1 when findings are detected, and 2 on an operational error.

# SANDBOX CHECKING

Many formatters only know how to fix a file in place; they have no read-only
"would this change?" mode. To check such a tool without touching the working
tree, conformist copies the candidate files into a sandbox, runs the formatter
there, and diffs the result against the originals. A finding is reported when
the sandbox copy differs.

A formatter that does have a native read-only check can declare it via
*check-command*, which conformist uses instead of the sandbox. Setting
*sandbox* to true forces the sandbox path even when a check command exists.
Because the source tree is never written during a check, *conformist check*
works against a read-only tree.

# FILE WALKING AND CACHING

The tree is traversed by a configurable walker: _git_, _jujutsu_, plain
_filesystem_, or _stdin_, selected by the *walk* option (_auto_ picks git or
jujutsu when present). To avoid redundant work, conformist records a per-file
signature — derived from the file's modification time and size together with
the sequence of tools applied to it — and skips files whose signature is
unchanged since the previous run. The cache is bypassed in check mode and by
the *--no-cache* and *--clear-cache* flags.

# CONFIGURATION

Configuration lives in a *conformist.toml*(5) file discovered by searching
upward from the working directory. conformist also reads the legacy
_treelint.toml_ name and honors the _TREELINT\__ environment prefix, easing
migration from the former treelint name.

# NIX MODULE

conformist ships a Nix module, modeled on treefmt-nix and extended to cover
linters. A flake declares its formatters and linters once and obtains a
generated *conformist.toml*(5), a *nix fmt* wrapper (repair mode), and a
read-only *conformist check* flake check, with tool binaries resolved from the
flake's pinned nixpkgs. A flake-parts module is also exported. conformist
self-consumes this module for its own formatting and linting.

# PROVENANCE

conformist is derived from numtide/treefmt v2.5.0 and is an independent
project, not a GitHub fork. treefmt's original copyright and MIT license are
retained; conformist is released under the MIT license.

# SEE ALSO

*conformist*(1), *conformist.toml*(5), *conformist-nix*(7),
*conformist-justfile*(7), *eng-design_patterns-conformist*(7)
