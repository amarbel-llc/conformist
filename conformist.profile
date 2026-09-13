---
# conformist profile (POC v1): the justfile convention linters, delivered as
# cache-pulled artifacts rather than through a Nix module. Hand-written; nothing
# consumes this yet. See docs/rfcs/0005.
! toml-conformist_profile-v1
---

# ---------------------------------------------------------------------------
# ARTIFACTS
#
# Pins are purpose-full markl-ids (piggy RFC 0011). The POC resolver verifies
# sha256 only; blake2b256 is registered for this purpose but refused.
#
# `just` is just-us v0.1.0's static (pkgsStatic/musl) build, a forge release
# asset. The pin was computed from the bytes the url actually serves
# (`just explore-markl-pin <url>`), not from a reported digest. The url uses the
# public code.linenisgreat.com host: the forge's own hostname puts release
# downloads behind a login redirect, which an unattended check cannot follow.
# ---------------------------------------------------------------------------

[artifact.just]
form = "static"
url = "https://code.linenisgreat.com/just-us/releases/download/v0.1.0/just-static-x86_64-unknown-linux-musl"
markl = "dodder-blob-digest-sha256-v1@sha256-yh7nfr5zsyr8s458qrunuwu5tas6ndy08ehwnsfgrlhg7h8muysqx8ftc5"

# NOTE: the rule logic is NOT an artifact here. It is authored alongside this
# profile, so it travels inline on the stanza below and inherits this document's
# integrity — no second pin to keep correct. A data artifact remains the right
# home for a rule that is large, shared between profiles, or build-produced
# (RFC 0005 §4.4).

# ---------------------------------------------------------------------------
# LINTER STANZAS
#
# Schema per RFC 0001, plus the inline `rule` field (RFC 0005 §4.4). The runner
# passes `rule` to the tool via a file or stdin — never through a shell command
# line, which is the whole point: the filter below may contain apostrophes and
# needs no escaping.
#
# One requirement this surfaced and the first draft missed: `just` must be
# reachable by BARE NAME, so the runner must put executable artifacts on PATH.
# Otherwise every command interpolates a path for its own tool.
# ---------------------------------------------------------------------------

[linter.justfile-recipe-names]
command = "just --dump --dump-format model"
rule-tool = "jq"
includes = ["justfile"]
passes-files = false
rule = '''
  ["build","test","validate","verify","lint","run","list","codemod","install",
   "deploy","load","migrate","provision","restart","bump","update","clean",
   "debug","explore"] as $verbs
  | ["default","tag","release"] as $exceptions
  | .recipes
  | map(select(.private | not))
  | .[]
  | .name as $name
  | (.name | split("-") | .[0]) as $verb
  | select(($exceptions | index($name)) == null)
  | select(($verbs | index($verb)) == null)
  | "'\(.namepath)' does not start with a known verb (conformist-justfile(7) VERB LIST)"
'''

# ---------------------------------------------------------------------------
# WHAT WRITING THIS FILE SETTLED
#
# A linter is not a tool. `justfile-recipe-names` is a pipeline over `just`
# (fork-only), `jq`, and a program that IS the rule. Delivering the fork's
# `just` delivered only a third of it; conformist now embeds jq (gojq) and runs
# it in-process, so a consumer needs no jq of its own.
#
# Both carriers for that program are REQUIRED (RFC 0005 §4.4), because they
# answer different questions about where the rule's integrity comes from:
#
#   INLINE, as above — the rule travels in this document, so whatever protects
#   the document protects the rule. No second pin to keep correct. Right for a
#   rule authored alongside its profile, which is most of them.
#
#   DATA ARTIFACT — fetched and pinned separately. Right when the rule is large,
#   shared across profiles, or build-produced, since then it does not travel in
#   the document and needs its own pin.
#
# Inlining into `command` stays REJECTED either way: the program would reach the
# tool through a shell command line, which is the quoting hazard that moving
# these filters into files removed in the first place.
#
# A stanza carrying a rule BOTH ways is an operational error, not a precedence
# question — otherwise a profile's effective rule would not be the one its
# author is reading.
#
# Consequence for v1: this profile needs no artifact-reference syntax at all,
# because its only rule is inline. That mechanism is still specified (§4.2) and
# still needed for the artifact case, but it is no longer on v1's critical path.
# ---------------------------------------------------------------------------
