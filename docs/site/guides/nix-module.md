# Nix module

conformist ships a Nix module — the same idea as
[treefmt-nix](https://github.com/numtide/treefmt-nix), extended to cover
conformist's linters. It lets a flake **declare its formatters and linters once**
and get, for free:

- a generated `conformist.toml` (`build.configFile`),
- a `nix fmt` entry point that runs conformist in repair mode (`build.wrapper`),
  and
- a read-only flake check that runs `conformist check` (`build.check`).

Tools resolve from your pinned nixpkgs, so you never hand-write `/nix/store`
paths.

!!! note "conformist is not in nixpkgs"
    The module has **no default `package`** — you MUST pass the conformist package
    yourself (from the conformist flake's output, or your own build). Every
    example below does so.

## Add the input

```nix
inputs.conformist.url = "github:amarbel-llc/conformist";
```

The flake exposes two consumption paths: `conformist.lib.evalModule` (plain
flakes) and `conformist.flakeModule` (flake-parts).

## Plain flake (`lib.evalModule`)

`lib.evalModule pkgs config` evaluates the module and returns
`config.build.{wrapper,check,configFile}`.

```nix
outputs =
  { self, nixpkgs, flake-utils, conformist }:
  flake-utils.lib.eachDefaultSystem (
    system:
    let
      pkgs = import nixpkgs { inherit system; };

      conformistEval = conformist.lib.evalModule pkgs {
        # REQUIRED — conformist is not in nixpkgs.
        package = conformist.packages.${system}.default;

        projectRootFile = "flake.nix";

        # Formatters (the programs.<name>.enable surface).
        programs.gofmt.enable = true;
        programs.nixfmt.enable = true;
        programs.prettier.enable = true;

        # Linters (the linters.<name>.enable surface, RFC 0001 §4).
        linters.shellcheck.enable = true;

        settings.excludes = [ "vendor/*" ];
      };
    in
    {
      # `nix fmt` -> repair mode (writes fixes).
      formatter = conformistEval.config.build.wrapper;

      # `nix flake check` -> read-only `conformist check`.
      checks.formatting = conformistEval.config.build.check self;
    }
  );
```

## flake-parts (`flakeModule`)

```nix
{
  imports = [ inputs.conformist.flakeModule ];

  perSystem = { pkgs, system, ... }: {
    conformist = {
      package = inputs.conformist.packages.${system}.default;

      programs.gofmt.enable = true;
      programs.nixfmt.enable = true;
      linters.shellcheck.enable = true;
    };
  };
}
```

By default this wires `formatter.<system>` (for `nix fmt`) and
`checks.<system>.conformist` (the read-only gate). Set `conformist.flakeFormatter`
or `conformist.flakeCheck` to `false` to opt out of either.

## Formatters vs linters

Formatters and linters live in **separate option namespaces** — `programs.*`
and `linters.*` — because a formatter and a linter MAY share a name and are
independent tools ([RFC 0001](../reference/formatter-spec.md) §4). For example,
`shellcheck` is a *linter* in conformist (read-only, reports findings), so it is
`linters.shellcheck`, not `programs.shellcheck`.

### Declaring a tool the module doesn't ship

The `programs.<name>` / `linters.<name>` enable surfaces cover the tools ported
from treefmt-nix. For anything else, write the `settings` table directly — the
same shape as a hand-written `conformist.toml`:

```nix
conformistEval = conformist.lib.evalModule pkgs {
  package = conformist.packages.${system}.default;

  # A formatter with a native read-only check.
  settings.formatter.myfmt = {
    command = "${pkgs.myfmt}/bin/myfmt";
    options = [ "--write" ];
    includes = [ "*.foo" ];
  };

  # A linter with an autofix (repair-command runs in repair mode).
  settings.linter.mylint = {
    command = "${pkgs.mylint}/bin/mylint";
    includes = [ "*.foo" ];
    "repair-command" = "${pkgs.mylint}/bin/mylint";
    "repair-options" = [ "--fix" ];
  };
};
```

!!! tip "Hyphenated keys"
    conformist reads hyphenated TOML keys (`repair-command`,
    `no-positional-arg-support`). In Nix these must be **quoted**:
    `"repair-command"`, not `repair-command`.

### Packaging a local script linter (sandbox-safe)

When a linter's `command` / `repair-command` is a script kept in your repo
(rather than a nixpkgs binary), package it with
`conformist.lib.writeCheckScript`. It installs the script, runs `patchShebangs`,
and prepends `runtimeInputs` to `PATH`:

```nix
conformistEval = conformist.lib.evalModule pkgs {
  package = conformist.packages.${system}.default;

  settings.linter.dead-jq =
    let
      deadJq = conformist.lib.writeCheckScript pkgs {
        name = "lint-dead-jq";
        src = ./scripts/lint-dead-jq;        # may start with #!/usr/bin/env bash
        runtimeInputs = [ pkgs.jq pkgs.gnugrep ];
      };
    in
    {
      command = "${deadJq}/bin/lint-dead-jq";
      includes = [ "*.jq" ];
    };
};
```

!!! warning "Why patchShebangs matters"
    A hand-rolled `cp script + wrapProgram` that forgets `patchShebangs` keeps
    the script's `#!/usr/bin/env bash` shebang. That **fails to exec inside the
    read-only `conformist check` sandbox** (the `checks.<name>` gate), where
    `/usr/bin/env` does not exist — and the failure is **masked outside the
    sandbox**, where a dev shell does have `/usr/bin/env`
    ([conformist#19](https://github.com/amarbel-llc/conformist/issues/19)).
    `writeCheckScript` resolves the shebang for you; if you must hand-roll, run
    `patchShebangs $out/bin` **before** `wrapProgram`.

### A toolchain-hermetic wrapper without the module (pre-commit hooks)

If you keep a **hand-written `conformist.toml`** (rather than generating config
from this module) but still want a pre-commit hook that doesn't depend on the
formatter toolchain being on the author's PATH, use
`conformist.lib.wrapWithToolchain`. It builds a wrapper that execs conformist
with `tools` on `PATH`, so `nix fmt`, `conformist check`, and the `--staged`
hook all run with the same pinned formatters:

```nix
conformistFmt = conformist.lib.wrapWithToolchain pkgs {
  conformist = conformist.packages.${system}.default;
  tools = [ pkgs.gotools pkgs.gofumpt pkgs.nixfmt pkgs.shfmt ]; # goimports ships in gotools
  name = "conformist-fmt";        # wrapper binary name
  configFile = ./conformist.toml;  # optional; pins --config-file
};
# expose it: formatter = conformistFmt; and put it on the devShell PATH.
```

Then the sweatfile hook just adds the staged flags to the wrapper:

```toml
[hooks]
pre-commit = "conformist-fmt --staged --exit-zero-on-fix"
```

!!! warning "Why the bare hook silently skips files"
    The canonical `pre-commit = "conformist --staged --exit-zero-on-fix"` runs
    the **bare** conformist, which resolves each formatter's `command` from
    `PATH`. If gofumpt/nixfmt/… aren't on the author's PATH at commit time, the
    staged repair **silently skips** those file types
    ([conformist#51](https://github.com/amarbel-llc/conformist/issues/51)).
    Module adopters avoid this with `build.preCommit` (store-pinned commands);
    a hand-written-config repo uses `wrapWithToolchain` as above. See
    `eng-design_patterns-conformist(7)` "THE CWD-AWARE WRAPPER".

## How the wrapper and check resolve the tree root

The two build outputs run conformist in different modes and resolve the tree root
differently — by design, because the two tree-root flags are mutually exclusive:

- **`build.wrapper`** (repair mode, `nix fmt`) runs the *wrapped* conformist with
  `--tree-root-file=<projectRootFile>` (default `flake.nix`). It runs from your
  live working directory, so it finds the real project root and may write fixes.
- **`build.check`** (read-only, the flake check) runs the *raw* conformist binary
  with an explicit `--tree-root=<source>` pointed at the project source. The
  explicit root matters: the generated config lives at a `/nix/store` path, and
  conformist would otherwise default its tree root to that config file's directory
  (i.e. `/nix/store`).

## Build outputs reference

| Output | What it is |
|--------|------------|
| `config.build.configFile` | The generated `conformist.toml` (a `/nix/store` path). |
| `config.build.wrapper` | A `conformist` wrapper that runs repair mode against the config. Use as `formatter.<system>`. |
| `config.build.check` | A function `self -> derivation` that runs `conformist check` read-only. Use as a `checks.<system>.*`. |
| `config.build.programs` | Attrset of the enabled formatter + linter packages (for a devShell). |
| `config.build.devShell` | A shell with the wrapper and all enabled tools on `PATH`. |

## Compatibility note

The generated file is named `conformist.toml`. conformist's own config
*discovery* prefers `conformist.toml` / `.conformist.toml`, falling back to the
legacy `treelint.toml` / `.treelint.toml` (the project's former name). The module
path is unaffected either way: the wrapper and check always pass `--config-file`
explicitly, so discovery never runs.
