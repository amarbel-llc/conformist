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
	When true, a configured tool that is not installed is skipped rather than
	failing the run.

*fail-on-change*
	When true, exit non-zero if any file was changed. Useful in CI.

*cpu-profile*
	Path to write a CPU profile to.

*verbose*
	Integer log verbosity (0 = warn, 1 = info, 2+ = debug).

# FORMATTER SECTIONS

Each _[formatter.<name>]_ table accepts:

*command*
	The executable to invoke. Required.

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
	The read-only check, run as _command [options] [files]_. It MUST NOT
	modify files and MUST exit non-zero when a passed file has a finding.
	Required.

*options*
	Arguments passed before the file list.

*repair-command*
	Optional autofix invocation run in repair mode. A linter with no
	*repair-command* is a no-op in repair mode.

*repair-options*
	Arguments passed to *repair-command*.

*passes-files*
	Defaults to true. Set false for a whole-tree check that runs once at the
	tree root with no file arguments, when at least one file matching its
	*includes* is present.

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
