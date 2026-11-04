# First-party owners burned into the profile (conformist#150 follow-up)

## Requirement

The first-party owner list of `flake-first-party-inputs` (default `amarbel-llc`,
`linenisgreat`, `friedenberg`) stays a FIXED list (see
`2026-11-03-first-party-flake-inputs-lint.md`), but it must be burned into the
conformist profile at release time so it is signed with the profile (RFC 0005
§3.2). Circus's `update-conformist-profile` re-signs the served profile when
`lib.profile`'s bytes change.

## What was verified (the profile route does NOT support this linter today)

- A profile `[linter.<name>]` stanza is a `command` plus, optionally, a jq `rule`
  run by in-process gojq over the command's stdout (`profile/resolve.go`,
  `rulejq/`). `rulejq` accepts only `-f FILE` over a stream of JSON on stdin.
- `flake-first-party-inputs` is a Nix-built bash script (awk/sed/grep/jq) that
  scans `flake.nix` as text and `flake.lock` as JSON, and supports warnings
  (`exempt-inputs`, `warn-only`). A profile rule emits findings only, with exit 1:
  no warnings, no text input.
- `profile.Parse` rejects any field it does not know (fail closed), so every new
  profile field breaks every older conformist that consumes the served profile.
- A profile is fleet-wide with no per-repo opt-in. Adding a linter stanza would
  turn the check on for every profile consumer, including known offenders
  (circus, troupe, eng), the moment it is re-signed.
- The Nix module path is the pure plane (sandboxed `checks.formatting`); it can
  read committed files at eval time but never fetches.

## Options

1. **Port the whole linter to a profile stanza** (jq over `flake.lock`, owners in
   the rule). Loses the `flake.nix` text scan, warnings and line numbers unless
   `rulejq` grows `-R`/`--rawfile`; and enables it fleet-wide unopted. Rejected
   as the first step.
2. **New profile data field** (`owners = [...]` on a table). Structured, but an
   unknown field to every older resolver, which then rejects the whole profile.
   Rejected for the compatibility cost (RFC 0005 §1, open question).
3. **A `[prelude.first-party-owners]` carrying a one-line jq `def`**, read by the
   Nix linter from `conformist.profile` at eval time. Needs no new field, so older
   resolvers are untouched; the unused prelude is never materialized, so profile
   consumers see no behaviour change; a later profile stanza can list it in
   `preludes` and share the same list. The Nix reader matches the exact one-line
   `def`, which is brittle by design: a reshaped prelude fails eval loudly
   (and a Go test pins the shape).

## Recommendation, implemented: option 3

- `conformist.profile` gains `[prelude.first-party-owners]`.
- `nix/linters/flake-first-party-inputs.nix` defaults `owners` to the list read
  from that file (`builtins.readFile`, `builtins.split`): offline and read-only.
  Setting `owners` still overrides per repo (replaces, does not extend).
- Tests: `TestCommittedProfileFirstPartyOwners` (prelude parses, evaluates to the
  list, keeps the one-line shape) and linter fixtures `profile-owner-fail`,
  `owners-override-pass`, `owners-override-fail`.

## Consequences for the operator

The profile bytes changed: `lib.profile` differs, so a re-sign through circus's
`update-conformist-profile` is due after this lands. The signature then covers the
owner list. Note the trust boundary: Nix-module consumers trust their flake lock,
not the signature; the signature protects profile-route consumers, and only once a
profile stanza lists the prelude (not done: that would enable the check fleet-wide).
