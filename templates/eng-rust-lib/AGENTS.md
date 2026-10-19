# AGENTS.md

Orientation for coding agents working in `EXAMPLE_CRATE`, a Rust library crate
formatted and linted by [conformist](https://code.linenisgreat.com/conformist).
`CLAUDE.md` is a symlink to this file.

## Commands

- `just` — the default lane: `test` then `lint`. It is also the pre-merge gate.
- `just test` — `cargo test --workspace`.
- `just lint` — read-only formatting check plus the impure lane (clippy with
  `-D warnings`, git remotes, sweatfile, this file's size).
- `just codemod-fmt` — format the tree in place (`nix fmt`).

Run these inside the devShell (`direnv` loads it); it carries cargo, rustc,
clippy, rustfmt and the pinned `just`.

## Layout

- `src/lib.rs` — the library root.
- `version.env` — the single source of the version.
- `conformist.nix` / `conformist-impure.nix` — the conformist configuration;
  the impure file holds the clippy lane.

Keep this file an orientation map: put reference prose in `doc/` and leave a
pointer here.
