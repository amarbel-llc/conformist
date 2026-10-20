# AGENTS.md

Orientation for coding agents working in `EXAMPLE_NAME`, a Go module built with
godyn and formatted and linted by
[conformist](https://code.linenisgreat.com/conformist). `CLAUDE.md` is a symlink
to this file.

## Commands

- `just` — the default lane: `build`, `test`, then `lint`. It is also the
  pre-merge gate.
- `just build` — build the binary (`nix build`).
- `just test` — godyn's per-package Go test lane.
- `just lint` — read-only formatting check plus the impure lane (git remotes,
  sweatfile, this file's size).
- `just codemod-fmt` — format the tree in place (`nix fmt`).

There is no ambient Go: dependencies live in `go.nix` (no `go.mod`), and go
commands run through `godyn-go` in the devShell (`direnv` loads it). See
`godyn(7)`.

## Layout

- `main.go` — the binary entry point (module root is the main package).
- `go.nix` — the module path, Go version and dependencies.
- `version.env` — the single source of the version.
- `conformist.nix` / `conformist-impure.nix` — the conformist configuration.

Keep this file an orientation map: put reference prose in `doc/` and leave a
pointer here.
