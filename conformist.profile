---
# conformist profile (POC v1): the justfile convention linters, delivered as
# cache-pulled artifacts rather than through a Nix module. Hand-written; nothing
# consumes this yet. See docs/rfcs/0005.
! toml-conformist_profile-v1
---

# ---------------------------------------------------------------------------
# ARTIFACTS
#
# Pins are purpose-full markl-ids (piggy RFC 0011). `url` is pending: just-us
# has not published the static build yet, and the pin cannot be computed until
# the exact published bytes exist. Verified separately that a pkgsStatic/musl
# `just` is genuinely static and emits a valid recipe-model payload
# (`just explore-static-just`), and that this pin format round-trips with real
# tooling (`just explore-markl-roundtrip`).
# ---------------------------------------------------------------------------

[artifact.just]
form = "static"
url = "PENDING: just-us static build, fleet cache"
markl = "PENDING: dodder-blob-digest-sha256-v1@blake2b256-..."

# The rule logic itself, as DATA. This is the artifact that decides whether the
# profile can carry linters at all — see the FINDING note at the bottom.
[artifact.justfile-recipe-names.filter]
form = "static"
executable = false
url = "PENDING"
markl = "PENDING"

# ---------------------------------------------------------------------------
# LINTER STANZAS
#
# Schema per RFC 0001. Two things here are NOT yet specified anywhere and are
# the real output of writing this file:
#
#   1. `just` must be on PATH for the command to resolve. Nothing says how an
#      executable artifact reaches PATH.
#   2. The command must name a data artifact without knowing its cache path.
#      `{{artifact.<name>}}` is invented here as a placeholder; RFC 0005 §4.2
#      requires that such a reference exist but does not define its syntax.
# ---------------------------------------------------------------------------

[linter.justfile-recipe-names]
command = "just --dump --dump-format model | jq -r -f {{artifact.justfile-recipe-names.filter}}"
includes = ["justfile"]
passes-files = false

# ---------------------------------------------------------------------------
# FINDING, recorded here because writing the file is what produced it
#
# A linter is not a tool. `justfile-recipe-names` is a shell pipeline over
# `just` (fork-only), `jq` (nixpkgs), and a jq PROGRAM FILE that IS the rule.
# Delivering the fork's `just` delivers roughly a third of it.
#
# The jq program has three possible homes, and only one is good:
#
#   (a) a data artifact, as above. Keeps the rule out of the command line, so
#       findings may contain apostrophes and no shell quoting is involved.
#       Costs: a second artifact per linter, and a path-reference syntax.
#
#   (b) inlined into `command`. TOML's ''' literal strings would hold the
#       program safely IN THE FILE, but the program still has to reach jq
#       through a shell command line, which is exactly the quoting hazard that
#       moving these filters to files removed earlier. Rejected.
#
#   (c) a separate non-command stanza field (e.g. `filter = '''...'''`), so the
#       runner passes it to the tool without a shell round-trip. Avoids both the
#       quoting hazard and the extra artifact, but invents new conformist config
#       surface and only helps tools that take a program on stdin/a file.
#
# (a) is what RFC 0005 §2.2 already mandates and what this file assumes. (c) is
# worth considering before v1 hardens, because it removes a whole artifact per
# linter and the reference syntax that goes with it.
# ---------------------------------------------------------------------------
