# Configure

`conformist`'s behaviour can be influenced in one of three ways:

1. Process flags and arguments
2. Environment variables
3. A [TOML] based config file

There is an order of precedence between these mechanisms as listed above, with process flags having the highest
precedence and values in the configuration file having the lowest.

!!!note

    Some options can **only be configured as process flags**,
    others may support **process flags and environment variables**,
    and others still may support **all three mechanisms**.

## Config File

The `conformist` configuration file is a mixture of global options and formatter sections.

It should be named `conformist.toml` or `.conformist.toml`, and typically resides at the root of a repository.

When executing `conformist` within a subdirectory, `conformist` will search upwards in the directory structure, looking for
`conformist.toml` or `.conformist.toml`.
You can change this behaviour using the [config-file](#config-file_1) options

!!! tip

    When starting a new project you can generate an initial config file using `conformist --init`

```nix title="conformist.toml"
--8<-- "cmd/init/init.toml"
```

## Global Options

### `allow-missing-formatter`

In check mode (`conformist check`), do not exit with an error if a configured
tool's binary is missing from PATH. Repair/format mode already degrades on a
missing tool by default — see [`require-tools`](#require-tools).

=== "Flag"

    ```console
    conformist check --allow-missing-formatter true
    ```

=== "Env"

    ```console
    CONFORMIST_ALLOW_MISSING_FORMATTER=true conformist check
    ```

=== "Config"

    ```toml
    allow-missing-formatter = true
    ```

### `require-tools`

In repair/format mode, exit with an error when a configured tool's binary is
missing from PATH instead of skipping that lane with a loud per-lane warning.
Repair/format mode degrades by default so a dep-bump repair is not blocked by an
unrelated missing tool; set this for repair gates that must run every lane.
(Check mode is strict regardless; relax it with
[`allow-missing-formatter`](#allow-missing-formatter).)

=== "Flag"

    ```console
    conformist --require-tools
    ```

=== "Env"

    ```console
    CONFORMIST_REQUIRE_TOOLS=true conformist
    ```

=== "Config"

    ```toml
    require-tools = true
    ```

### `ci`

Runs conformist in a CI mode, enabling [no-cache](#no-cache), [fail-on-change](#fail-on-change) and adjusting some other settings best suited to a
continuous integration environment.

=== "Flag"

    ```console
    conformist --ci
    ```

=== "Env"

    ```console
    CONFORMIST_CI=true conformist
    ```

### `clear-cache`

Reset the evaluation cache. Use in case the cache is not precise enough.

=== "Flag"

    ```console
    conformist -c
    conformist --clear-cache
    ```

=== "Env"

    ```console
    CONFORMIST_CLEAR_CACHE=true conformist
    ```

### `config-file`

=== "Flag"

    ```console
    conformist --config-file /tmp/conformist.toml
    ```

=== "Env"

    ```console
    CONFORMIST_CONFIG=/tmp/conformist.toml conformist
    ```

### `cpu-profile`

The file into which a [pprof](https://github.com/google/pprof) cpu profile will be written.

=== "Flag"

    ```console
    conformist --cpu-profile ./cpu.pprof
    ```

=== "Env"

    ```console
    CONFORMIST_CPU_PROFILE=./cpu.pprof conformist
    ```

=== "Config"

    ```toml
    cpu-profile = "./cpu.pprof"
    ```

### `excludes`

An optional list of [glob patterns](#glob-patterns-format) used to exclude files from all formatters.

=== "Flag"

    ```console
    conformist --excludes *.toml,*.php,README
    ```

=== "Env"

    ```console
    CONFORMIST_EXCLUDES="*.toml,*.php,README" conformist
    ```

=== "Config"

    ```toml
    excludes = ["*.toml", "*.php", "README"]
    ```

### `fail-on-change`

Exit with error if any changes were made during execution.

=== "Flag"

    ```console
    conformist --fail-on-change true
    ```

=== "Env"

    ```console
    CONFORMIST_FAIL_ON_CHANGE=true conformist
    ```

=== "Config"

    ```toml
    fail-on-change = true
    ```

### `formatters`

A list of formatters to apply.
Defaults to all configured formatters.

=== "Flag"

    ```console
    conformist -f go,toml,haskell
    conformist --formatters go,toml,haskell
    ```

=== "Env"

    ```console
    CONFORMIST_FORMATTERS=go,toml,haskell conformist
    ```

=== "Config"

    ```toml
    formatters = ["go", "toml", "haskell"]

    [formatter.go]
    ...

    [formatter.toml]
    ...

    [formatter.haskell]
    ...

    [formatter.ruby]
    ...

    [formatter.shellcheck]
    ...
    ```

### `no-cache`

Ignore the evaluation cache entirely. Useful for CI.

=== "Flag"

    ```console
    conformist --no-cache
    ```

=== "Env"

    ```console
    CONFORMIST_NO_CACHE=true conformist
    ```

### `on-unmatched`

Log paths that did not match any formatters at the specified log level.
Possible values are `<debug|info|warn|error|fatal>`.

!!! warning

    If you select `fatal`, the process will exit immediately with a non-zero exit.

=== "Flag"

    ```console
    conformist -u debug
    conformist --on-unmatched debug
    ```

=== "Env"

    ```console
    CONFORMIST_ON_UNMACTHED=info conformist
    ```

=== "Config"

    ```toml
    on-unmatched = "debug"
    ```

### `quiet`

Suppress all output except for errors.

=== "Flag"

    ```console
    conformist --quiet
    ```

=== "Env"

    ```console
    CONFORMIST_QUIET=true conformist
    ```

### `stdin`

Format the context passed in via stdin.

!!! note
You must provide a single path argument, the value of which is used to match against the configured formatters.

=== "Flag"

    ```console
    cat ../test.go | conformist --stdin foo.go
    ```

### `tree-root`

The root directory from which conformist will start walking the filesystem.
Defaults to the directory containing the config file.

=== "Flag"

    ```console
    conformist --tree-root /tmp/foo
    ```

=== "Env"

    ```console
    CONFORMIST_TREE_ROOT=/tmp/foo conformist
    ```

=== "Config"

    ```toml
    tree-root = "/tmp/foo"
    ```

### `tree-root-cmd`

Command to run to find the tree root.
It is parsed using [shlex](https://github.com/google/shlex/tree/master), to allow quoting arguments that contain whitespace.
If you wish to pass arguments containing quotes, you should use nested quotes e.g. `"'"` or `'"'`.

!!!note

    If [walk](#walk) is set to `git` and no tree root option has been defined, `tree-root-cmd` will be defaulted to
    `git rev-parse --show-toplevel`.

    If [walk](#walk) is set to `jujutsu` and no tree root option has been defined, `tree-root-cmd` will be defaulted to
    `jj workspace root`.

    if [walk](#walk) is set to `auto` (the default), `conformist` will check if the [working directory](#working-dir) is
    inside a git worktree. If it is, `tree-root-cmd` will be defaulted as described above for `git`. If the [working
    directory](#working-dir) is inside a jujutsu worktree the `tree-root-cmd` will be defaulted as described above for
    `jujutsu`.

=== "Flag"

    ```console
    conformist --tree-root-cmd "git rev-parse --show-toplevel"
    ```

=== "Env"

    ```console
    CONFORMIST_TREE_ROOT_CMD="git rev-parse --show-toplevel" conformist
    ```

=== "Config"

    ```toml
    tree-root-cmd = "git rev-parse --show-toplevel"
    ```

### `tree-root-file`

File to search for to find the tree root (if `tree-root` is not set)

=== "Flag"

    ```console
    conformist --tree-root-file .git/config
    ```

=== "Env"

    ```console
    CONFORMIST_TREE_ROOT_FILE=.git/config conformist
    ```

=== "Config"

    ```toml
    tree-root-file = ".git/config"
    ```

### `verbose`

Set the verbosity level of logs:

- `0` => `warn`
- `1` => `info`
- `2` => `debug`

=== "Flag"

    The number of `v`'s passed matches the level set.

    ```console
    conformist -vv
    ```

=== "Env"

    ```console
    CONFORMIST_VERBOSE=1 conformist
    ```

=== "Config"

    ```toml
    verbose = 2
    ```

### `walk`

The method used to traverse the files within the tree root.
Currently, we support 'auto', 'git', 'jujutsu' or 'filesystem'

=== "Flag"

    ```console
    conformist --walk filesystem
    ```

=== "Env"

    ```console
    CONFORMIST_WALK=filesystem conformist
    ```

=== "Config"

    ```toml
    walk = "filesystem"
    ```

### `working-dir`

Run as if `conformist` was started in the specified working directory instead of the current working directory.

=== "Flag"

    ```console
    conformist -C /tmp/foo
    conformist --working-dir /tmp/foo
    ```

=== "Env"

    ```console
    CONFORMIST_WORKING_DIR=/tmp/foo conformist
    ```

## Formatter Options

Formatters are configured using a [table](https://toml.io/en/v1.0.0#table) entry in `conformist.toml` of the form
`[formatter.<name>]`:

```toml
[formatter.alejandra]
command = "alejandra"
includes = ["*.nix"]
excludes = ["examples/nix/sources.nix"]
priority = 1

[formatter.deadnix]
command = "deadnix"
options = ["-e"]
includes = ["*.nix"]
priority = 2
```

### `command`

The command to invoke when applying the formatter.

### `options`

An optional list of args to be passed to `command`.

### `includes`

A list of [glob patterns](#glob-patterns-format) used to determine whether the formatter should be applied against a given path.

### `excludes`

An optional list of [glob patterns](#glob-patterns-format) used to exclude certain files from this formatter.

### `priority`

Influences the order of execution. Greater precedence is given to lower numbers, with the default being `0`.

### `no-positional-arg-support`

If `true`, `conformist` will invoke the formatter with no more than 1 file at a time.

Enable this if the formatter can only format 1 file at a time (a violation of
[rule 1 of the formatter spec](https://treefmt.com/latest/reference/formatter-spec/#1-files-passed-as-arguments)).

## Same file, multiple formatters?

For each file, `conformist` determines a list of formatters based on the configured `includes` / `excludes` rules. This list is
then sorted, first by priority (lower the value, higher the precedence) and secondly by formatter name (lexicographically).

The resultant sequence of formatters is used to create a batch key, and similarly matched files get added to that batch
until it is full, at which point the files are passed to each formatter in turn.

This means that `conformist` **guarantees only one formatter will be operating on a given file at any point in time**.
Another consequence is that formatting is deterministic for a given file and a given `conformist` configuration.

By setting the priority fields appropriately, you can control the order in which those formatters are applied for any
files they _both happen to match on_.

## Glob patterns format

This is a variant of the Unix glob pattern. It supports all the usual
selectors such as `*` and `?`.

### Examples

- `*.go` - match all files in the project that end with a ".go" file extension.
- `vendor/*` - match all files under the vendor folder, recursively.

## Supported Formatters

Any formatter that follows the [spec] is supported out of the box.

Already 60+ formatters are supported.

For per-formatter config examples, treefmt-nix's catalog is a useful reference:
<https://github.com/numtide/treefmt-nix/tree/main/examples>.

If you are a Nix user, see the [Nix module guide](../guides/nix-module.md):
conformist ships its own module that pulls in the right formatter and linter
packages from your pinned nixpkgs and wires up `nix fmt` plus a read-only flake
check.

[spec]: ../reference/formatter-spec.md
[TOML]: https://toml.io
