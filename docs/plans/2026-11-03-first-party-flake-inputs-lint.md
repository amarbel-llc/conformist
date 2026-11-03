# First-party flake inputs on the forge (conformist#150)

## Problem

`nix flake update` makes one api.github.com call per floating `github:` input.
GitHub's rate limit halted the circus update cascade twice on 2026-11-03.
First-party code lives on code.linenisgreat.com, so first-party inputs should not
touch GitHub at all.

## Design

One linter, `flake-first-party-inputs` (`nix/linters/flake-first-party-inputs.nix`),
whole-tree, reading only committed files, so it runs in the sandboxed
`checks.formatting` lane.

- **R1, the rule.** An input whose owner is in `owners` (default `amarbel-llc`,
  `linenisgreat`, `friedenberg`) fails when written as `github:O/R`,
  `git+https://github.com/O/R`, `git+ssh://git@github.com/O/R` (or `ssh://`,
  `git@github.com:`), or `https://github.com/O/R/archive/...`. The message names
  the input and `https://code.linenisgreat.com/<repo>/archive/<ref>.tar.gz`.
- **R2, where.** `flake.nix` is scanned as text for the declared URL (error);
  `flake.lock` is scanned for every node's `original`. A direct input there is an
  error; a transitive first-party GitHub node is a warning against the direct
  input that reaches it (the fix lives in that input's repo or a `follows`).
- **R3, later, not built.** An allowlist rule failing on any `github:` input
  outside e.g. `NixOS/nixpkgs`. The per-node classification in the lock pass is
  the one place it would hook in, and the `owners`/`forge-url` options already
  parameterise the message.

Owners are an option with a fixed default, not a live PAPI lookup: the check must
run offline in a sandbox. A repo can override `owners`.

## How eng is exempted

The linter is **not** added to the `eng` preset, so no repo gets it implicitly.
eng enables it with `warn-only = true` (every direct violation becomes a
warning, exit 0), or `exempt-inputs = [ "bats" "nixpkgs" "ssh-agent-mux" ]` for
the three inputs ruled to wait for FDR 0044 stage 5. At stage 5, eng drops the
exemption. Enabling it in eng is eng's change, not this one.

## Reuse of doppelgang's canonical-inputs check

Not reused. It is a repair check taking `--papi-domain`: it rewrites inputs, which
touches locks, and needs a live PAPI source. This lint is read-only, offline, and
adds the lock-transitive warning. If the fleet later wants repair, doppelgang's
check is the natural repair half and could consume the same owner list.

## Known offenders (other repos' work)

circus `sites/linenisgreat`, troupe (via igloo), eng. No locks are changed here.
