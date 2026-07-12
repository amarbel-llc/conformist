# Install

conformist is built with Nix; the flake is the supported install path. A plain
Go build also works for development.

## With Nix (recommended)

Run directly from the flake:

```
nix run 'git+https://code.linenisgreat.com/conformist.git' -- --help
```

Or build the package into `./result`:

```
nix build 'git+https://code.linenisgreat.com/conformist.git'
./result/bin/conformist --help
```

## From source (Go)

conformist requires Go 1.26 or newer.

```
git clone https://code.linenisgreat.com/conformist.git
cd conformist
go build -o conformist .
./conformist --help
```

Inside the repository's dev shell (`nix develop`), `just build` runs the full
gomod2nix + Go + Nix build and `just test` runs the Go tests. Use the
`conformist check` subcommand to run linters and formatters read-only.
