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

# Each rule opens with the same few definitions as just-us's shared jq prelude
# (nix/justfile-model.nix): the recipe-model schema/version pin, and the
# private-recipe exemption. The pin is not optional. A rule reading an absent
# field such as `doc_prelude` would otherwise pass vacuously against any `just`
# that emitted a different model. A profile has no way to share definitions
# between inline rules, so they are COPIED per rule — a gap this tracer
# surfaced; see the notes at the end of this file.

[linter.justfile-recipe-names]
command = "just --dump --dump-format model"
rule-tool = "jq"
includes = ["justfile"]
passes-files = false
rule = '''
  def model:
    if .schema != "just-us.recipe-model" then
      error("unexpected schema '\(.schema // "<absent>")'; expected 'just-us.recipe-model'")
    elif .version != 1 then
      error("unsupported recipe-model version '\(.version // "<absent>")'; this rule pins version 1")
    else . end;
  def public: map(select(.private | not));

  ["build","test","validate","verify","lint","run","list","codemod","install",
   "deploy","load","migrate","provision","restart","bump","update","clean",
   "debug","explore"] as $verbs
  | ["default","tag","release"] as $exceptions
  | model
  | .recipes
  | public
  | .[]
  | .name as $name
  | (.name | split("-") | .[0]) as $verb
  | select(($exceptions | index($name)) == null)
  | select(($verbs | index($verb)) == null)
  | "'\(.namepath)' does not start with a known verb (conformist-justfile(7) VERB LIST)"
'''

# conformist-justfile(7) RECIPE DESCRIPTIONS: `just --list` shows only the ONE
# comment line directly above a recipe, so a block of comment lines leaves the
# rest invisible and the description a truncated fragment. Reads the fork-only
# `doc_prelude` field. Covers every public recipe, debug/explore included, and
# has no repair: the fix is editorial. Ported from just-us's
# justfile-orphan-summary module.
[linter.justfile-orphan-summary]
command = "just --dump --dump-format model"
rule-tool = "jq"
includes = ["justfile"]
passes-files = false
rule = '''
  def model:
    if .schema != "just-us.recipe-model" then
      error("unexpected schema '\(.schema // "<absent>")'; expected 'just-us.recipe-model'")
    elif .version != 1 then
      error("unsupported recipe-model version '\(.version // "<absent>")'; this rule pins version 1")
    else . end;
  def public: map(select(.private | not));

  model
  | .recipes
  | public
  | .[]
  | select((.doc_prelude | length) > 0)
  | "recipe '\(.namepath)' has comment lines above its doc comment; `just --list` shows ONLY the last comment line, so the rest is invisible and the description reads as a truncated fragment - separate the prose from the one-line summary with a bare `#` line (or a blank line), and make that summary a whole sentence fragment that stands alone (conformist-justfile(7) RECIPE DESCRIPTIONS)"
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
# because its rules are inline. That mechanism is still specified (§4.2) and
# still needed for the artifact case, but it is no longer on v1's critical path.
#
# Porting a SECOND rule surfaced a gap the first could not: shared definitions.
# just-us's Nix modules prepend one jq prelude to every rule. A profile cannot,
# so each rule carries its own copy of the model pin, and eight rules would mean
# eight copies to keep in step. Neither carrier fixes this today — a
# `rule-artifact` replaces the whole rule, it does not prefix one. Candidates
# for RFC 0005: a per-rule-tool prelude table, or multiple rule artifacts
# concatenated in order.
# ---------------------------------------------------------------------------
