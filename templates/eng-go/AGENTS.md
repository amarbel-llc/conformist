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
- `just lint` — read-only formatting check, `lint-layout` (package levels),
  `lint-manpage-names` (manpage NAME lines), plus the impure lane (git remotes,
  sweatfile, this file's size).
- `just codemod-fmt` — format the tree in place (`nix fmt`).

There is no ambient Go: dependencies live in `go.nix` (no `go.mod`), and go
commands run through `godyn-go` in the devShell (`direnv` loads it). See
`godyn(7)`.

## Layout

- `main.go` — the binary entry point (module root is the main package).
- `internal/<level>/<leaf>` — library packages, by import depth (see below).
- `doc/` — manpage sources; `doc/cli.1.scd` is the binary's section-1 page.
- `go.nix` — the module path, Go version and dependencies.
- `version.env` — the single source of the version.
- `conformist.nix` / `conformist-impure.nix` — the conformist configuration.

## Package levels (LAYOUT)

A package under `internal/` sits in the directory named for its dependency
height, which `dagnabit` computes from its imports within this module: `0` for a
package that imports no other package of this module, then `alfa`, `bravo`,
`charlie`, ... for one more level each. `internal/0/buildinfo` is the example.

To add a package: create `internal/<level>/<leaf>/` where `<level>` is one above
the highest level among its imports. When an import changes a package's height,
`just lint-layout` fails and names each package and the level to move it to;
`git mv` it there and fix the import paths. The check does not see a package
with no import edges at all.

## Manpages

Pages are scdoc sources in `doc/` (`<name>.<section>.scd`), built by the
`manpages` package and installed by the default package under `share/man`.
`doc/cli.1.scd` is installed under the package's name. Each page's first line
under NAME is `name - summary` on one line; `just lint-manpage-names` enforces
that fleet contract.

Keep this file an orientation map: put reference prose in `doc/` and leave a
pointer here.
