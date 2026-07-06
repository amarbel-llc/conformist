conformist.toml(5)

# NAME

conformist.toml - configuration file for *conformist*(1)

# DESCRIPTION

*conformist*(1) is configured by a TOML file that declares the formatters and
linters to run over a tree, plus tree-wide options. The file is discovered by
searching upward from the working directory for, in order of preference:

```
conformist.toml
.conformist.toml
treelint.toml
.treelint.toml
```

The _treelint_ names are a backward-compatible fallback from before the
treelint to conformist rename. Discovery is overridden by the *--config-file*
flag or the _CONFORMIST_CONFIG_ environment variable, and is also attempted in
_PRJ_ROOT_ when that variable is set.

The two tool kinds are declared in parallel tables: _[formatter.<name>]_ for
formatters (which rewrite files in place) and _[linter.<name>]_ for linters
(whose check command is read-only, with an optional repair command). A
formatter and a linter MAY share a name. See *conformist*(7) for the conceptual
model and RFC 0001 for the linter design.

# TOP-LEVEL OPTIONS

These keys appear at the top level of the file. Operational flags such as
*--ci*, *--no-cache*, *--clear-cache*, *--quiet*, *--stdin* and *--working-dir*
are command-line only and are rejected in the config file.

*excludes*
	List of glob patterns excluded from every formatter and linter. Preferred
	over the deprecated _[global]_ *excludes* table.

*formatters*
	List of formatter names to run. Defaults to every configured formatter.

*walk*
	Tree traversal method: one of _auto_, _git_, _jujutsu_, _filesystem_.
	Defaults to _auto_.

*tree-root*, *tree-root-cmd*, *tree-root-file*
	Mutually exclusive ways to locate the tree root. Without any of them the
	root is the enclosing git or jujutsu worktree, falling back to the config
	file's directory.

*on-unmatched*
	Log level for paths matching no formatter: _debug_, _info_, _warn_,
	_error_, or _fatal_. Defaults to _info_.

*allow-missing-formatter*
	In check mode (*conformist check*), skip a configured tool whose binary is
	not installed rather than failing the check. Repair/format mode already
	degrades on a missing tool by default — see *require-tools*.

*require-tools*
	In repair/format mode, fail the run when a configured tool's binary is
	missing from PATH instead of skipping that lane with a loud per-lane warning
	(conformist#75). Use for repair gates that must run every lane. Check mode is
	strict regardless (relax it with *allow-missing-formatter*).

*fail-on-change*
	When true, exit non-zero if any file was changed. Useful in CI.

*cpu-profile*
	Path to write a CPU profile to.

*verbose*
	Integer log verbosity (0 = warn, 1 = info, 2+ = debug).

# FORMATTER SECTIONS

Each _[formatter.<name>]_ table accepts:

*command*
	The tool to invoke. Required. Either a single executable (looked up on
	PATH, as before) or a shell line --- e.g. _cd sub && tool_ --- run through an
	in-process shell interpreter. A shell line may use _cd_, _&&_, and pipes,
	and receives the matched files as positional parameters (reference them as
	"$@"); a bare single-word command keeps the direct-exec fast path. See also
	*working-dir* for the common "run in a subdirectory" case.

*options*
	List of arguments passed before the file list.

*includes*
	List of glob patterns selecting files this formatter handles.

*excludes*
	List of glob patterns excluding files from this formatter.

*priority*
	Integer ordering within a file's formatter sequence; lower runs first.

*check-command*
	A native read-only check used by *conformist check* instead of the
	sandbox-and-diff strategy.

*check-options*
	Arguments passed to *check-command*.

*sandbox*
	Force sandbox checking in check mode even when *check-command* is set.

*no-positional-arg-support*
	Set true for a tool that cannot accept a file list as arguments.

*working-dir*
	Subdirectory, relative to the tree root, to run this formatter in --- e.g.
	a submodule whose codegen must run from there. Defaults to the tree root.
	When set, file arguments are passed relative to it.

# LINTER SECTIONS

Each _[linter.<name>]_ table accepts the same *includes*, *excludes*,
*priority*, *no-positional-arg-support* and *working-dir* keys as a formatter,
plus:

*command*
	The read-only check, run as _command [options] [files]_. May be a single
	executable or a shell line (see the formatter *command*). It MUST NOT
	modify files and MUST exit non-zero when a passed file has a finding.
	Required.

*options*
	Arguments passed before the file list.

*repair-command*
	Optional autofix invocation run in repair mode --- a single executable or a
	shell line, like *command*. A linter with no *repair-command* is a no-op in
	repair mode.

*repair-options*
	Arguments passed to *repair-command*.

*passes-files*
	Defaults to true. Set false for a whole-tree check that runs once at the
	tree root with no file arguments, when at least one file matching its
	*includes* is present.

	*passes-files* also governs the top-level *excludes* interaction. A per-file
	linter (true) skips globally-excluded files, like formatters, because it
	would receive the file as input. A whole-tree check (false) matches them ---
	its *includes* are only a trigger gate and the file is never passed to it ---
	so it can watch a file that formatters are forbidden to rewrite, e.g. a drift
	check triggering on _go.mod_. (Earlier releases used a separate
	*ignore-global-excludes* flag for this; it was retired in favor of
	*passes-files*.)

*restage-repair-outputs*
	Defaults to false. Set true on a whole-tree codegen-repair linter
	(*passes-files* = false with a *repair-command*) to widen the *--staged*
	hook's restage scope to the files its repair regenerates.

	The *--staged* hook (see *conformist*(7)) otherwise restages only files that
	were already in the index, which is correct for formatters and per-file
	linters but strands the outputs of a whole-tree repair that regenerates files
	_other_ than the staged ones --- e.g. editing a staged source makes a
	generated sibling stale; the repair regenerates the sibling, but it was never
	staged, so the commit lands stale. With this set, *--staged* also restages the
	paths this linter's *repair-command* actually wrote, detected by a git-status
	delta taken around that linter's repair so only its own writes are captured
	(not another linter's). Ignored unless the linter is whole-tree
	(*passes-files* = false) and has a *repair-command*.

	Only modified _tracked_ outputs are restaged by this flag: staging a
	brand-new (untracked) output is the separate *stage-new-outputs* opt-in
	(below), and staging a deletion the repair performs is *stage-deleted-outputs*.

*stage-new-outputs*
	Defaults to false. Effective only when *restage-repair-outputs* is also true.
	When both are set, *--staged* additionally stages brand-new (untracked) files
	the linter's *repair-command* creates, in addition to the modified tracked
	outputs *restage-repair-outputs* covers.

	Staging untracked files is more dangerous than restaging tracked ones --- a
	buggy or over-broad codegen could sweep unintended new files into history ---
	so it is a distinct opt-in: *restage-repair-outputs* alone never stages
	untracked outputs. Attribution is by the same git-status delta around the
	linter's repair, so a pre-existing untracked file is never swept in.

*stage-deleted-outputs*
	Defaults to false. Effective only when *restage-repair-outputs* is also true.
	When both are set, *--staged* stages the deletions the linter's
	*repair-command* performs (e.g. a package-move codegen that removes a
	relocated file), so the removal is part of the commit rather than left as an
	unstaged deletion.

	Staging a deletion removes a path from the commit's tree --- the most
	destructive stage mutation --- so it is a distinct opt-in:
	*restage-repair-outputs* and *stage-new-outputs* never stage deletions. Like
	the other tiers, attribution is by the git-status delta around this linter's
	repair.

# ENVIRONMENT

Every option has an environment override named by upper-casing the key and
prefixing _CONFORMIST\__ (for example _CONFORMIST_WALK_, _CONFORMIST_EXCLUDES_).
The legacy _TREELINT\__ prefix is honored when the _CONFORMIST\__ equivalent is
unset.

*CONFORMIST_CONFIG*
	Path to the config file, overriding upward discovery.

*CONFORMIST_CEILING_DIRECTORIES*
	A colon-separated list of absolute paths at which the upward search for a
	config file stops, mirroring *git*'s _GIT_CEILING_DIRECTORIES_: conformist
	will not ascend into any listed directory (so that directory and everything
	above it is never searched), though the starting directory is always
	searched. Entries are symlink-resolved for comparison; an empty entry turns
	off resolution for the entries that follow it. Use it to stop conformist
	picking up a config from outside the project (e.g. an enclosing monorepo).

# EXAMPLES

```
[formatter.nix]
command = "nixfmt"
includes = ["*.nix"]

[formatter.rust]
command = "rustfmt"
options = ["--edition", "2018"]
includes = ["*.rs"]

[linter.shellcheck]
command = "shellcheck"
includes = ["*.sh"]

[linter.ruff]
command = "ruff"
options = ["check"]
repair-command = "ruff"
repair-options = ["check", "--fix"]
includes = ["*.py"]
```

# SEE ALSO

*conformist*(1), *conformist*(7)
