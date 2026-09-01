---
status: proposed
date: 2026-09-01
---

# The conformist Profile: Cache-Delivered Linter Tools

## Abstract

This document specifies the **conformist profile**: a data-only document that
names the external tools a repository's linters and formatters need, and the
pre-built artifacts those tools are delivered as. A profile entry pins an
artifact by URL and content hash; conformist downloads it, verifies the hash,
materializes it into a content-addressed cache directory, and executes it from
there. Tool delivery therefore never enters a Nix evaluation graph, which makes
a class of dependency cycle — a linter that needs a tool from a repository that
itself consumes conformist — structurally impossible rather than merely
discouraged.

## Introduction

conformist currently delivers linter tooling through Nix modules: a linter is a
`writeShellApplication` whose `runtimeInputs` are resolved at evaluation time,
and a consumer obtains it by importing a module and taking a flake input.

That works until the tool lives downstream of conformist. The `justfile-*`
convention linters are the motivating case: they read `just --dump --dump-format
model`, a format that only the just-us fork emits, so the linter modules must be
able to reach a just-us build. But just-us already takes conformist as a flake
input, so conformist cannot take one back. The rules were moved into just-us to
break the cycle (conformist#85, conformist#89), which resolved it for adopters
and left conformist itself unable to lint its own justfile — it authors
conformist-justfile(7), the normative home for those conventions, without being
held to them.

Two rejected alternatives are worth recording, because both look reasonable
until examined:

- **Folding the roster into `conformist.lib.presets.eng`.** A system-independent
  module path cannot close over a system-specific derivation, so the preset
  could not supply the tool anyway; and because the shared package option is
  mandatory, enabling it would turn a silent loss of rules into a simultaneous
  eval failure across every existing adopter.
- **A fixed-output source pin of just-us inside conformist.** Technically works
  and was briefly implemented, but it puts a `fetchgit` plus a full Rust build
  into the lint closure of anything that consumes it, and reintroduces the very
  coupling the move was meant to remove.

The profile replaces evaluation-time module delivery with **runtime
materialization**. This specification defines the profile document, how it
resolves across directory layers, the artifact forms it may name, and how the
linter configuration it carries merges with configuration from other sources.

Background: eng FDR 0015 (umbrella), RFC 0001 (the `[linter.<name>]` section and
check/repair modes), RFC 0004 (flakeclobber's refuse-on-ambiguity contract).

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### 1. The profile document

A profile is a hyphence document. Its type tag MUST be `conformist-profile-v1`.
Its body MUST be TOML.

The `v1` suffix is the schema version. An implementation MUST reject a profile
whose type tag it does not recognize, and MUST report the tag it found; it MUST
NOT attempt a partial read of an unknown version. Additive fields within a
version do not change the tag.

A profile MAY carry a locked `-` reference delegating to another profile (§3).

### 2. Artifacts

The `[artifact.<name>]` table names one delivered artifact. `<name>` MUST be
unique within the merged profile.

Every artifact entry MUST declare a `form`. Three forms are defined:

| `form` | Required fields | Status |
|---|---|---|
| `static` | `url`, `hash` | REQUIRED — every implementation MUST support it |
| `oci` | `image`, `digest` | OPTIONAL escape hatch |
| `drv` | `store-path` (or a means of realizing one) | OPTIONAL escape hatch |

`static` is the primary form and the only one the POC (§7) covers.
Implementations MUST support `static`; they MAY support the others.

`hash` MUST be a SRI string. After download and before any use, the
implementation MUST verify the artifact against `hash`, and MUST fail the run
with an operational error (exit 2) on mismatch. It MUST NOT execute, cache, or
partially apply an artifact that failed verification.

#### 2.1 Executability, and why `static` is the primary form

An artifact materialized outside `/nix/store` cannot rely on a Nix-built dynamic
loader or on `runtimeInputs` being present. A `static` artifact therefore MUST be
a self-contained executable with no dynamic dependencies beyond the host kernel
ABI, or a non-executable data file (§2.2).

This constraint is verified, not assumed: a `pkgsStatic`/musl build of just-us's
`just` links statically (`ldd` reports `statically linked`) and emits a valid
`just-us.recipe-model` v1 payload.

#### 2.2 Artifacts are not necessarily executables

An implementation MUST support artifacts that are **data files**, not just
executables. An artifact entry MAY declare `executable = false`; the default is
`true`.

This requirement is load-bearing rather than speculative. A linter is generally
not a single binary. Each `justfile-*` linter is a shell program that invokes
`just`, `jq`, and coreutils, and passes `jq` a **program file** via `jq -f`. That
file is a data artifact. It is deliberately a file and not an inline string:
embedding the filter in a shell command line reintroduces a quoting hazard class
(findings containing apostrophes, and the shellcheck suppressions that come with
a single-quoted embedded program) that moving it to a file removed.

An implementation that supported only executable artifacts would therefore force
every such linter back into embedded-string filters. Data artifacts MUST be
materialized into the cache and MUST be referenceable from a stanza's command
(§4.2), but MUST NOT be marked executable in the cache.

### 3. Resolution

Profile resolution is an **upward walk**, in the manner of `.editorconfig`.

Starting from the tree root under inspection, an implementation MUST walk
upward through parent directories collecting profile documents, and MUST stop at
the filesystem root or at a profile declaring itself terminal. Layers MUST be
merged nearest-last, so that **the closest layer wins** for any key defined more
than once.

Merge is per-key, not per-table: a nearer layer overriding one field of an
`[artifact.<name>]` or `[linter.<name>]` table MUST NOT discard the sibling
fields defined by a farther layer.

#### 3.1 Locked delegation

A profile MAY delegate to an organization baseline served by papi, via a hyphence
`-` reference of the form `- <object-id> < <markl-id>`.

The reference MUST be **locked**: it MUST pin a specific object identity, and an
implementation MUST verify the fetched baseline against it. An implementation
MUST NOT follow an unlocked or floating delegation. A delegation that fails to
resolve or verify MUST fail the run with an operational error; an implementation
MUST NOT silently continue with the local layers alone, because doing so would
quietly drop every rule the baseline contributes.

Delegated layers MUST be treated as farther than every local layer, so a
repository can always override its organization baseline locally.

### 4. Linter configuration in the profile

#### 4.1 Stanzas

A profile MAY carry `[linter.<name>]` and `[formatter.<name>]` tables. Their
schema is that of RFC 0001; this document does not redefine it.

This is the mechanism by which Nix modules stop being the delivery vehicle for
sibling-tool linters: the rule's configuration travels as data in the profile,
alongside the artifacts it needs.

#### 4.2 Referring to artifacts from a stanza

A stanza MUST be able to name an artifact without knowing its cache path.
Implementations MUST make each materialized artifact resolvable by its
`<name>`, and MUST do so without requiring the profile author to write an
absolute path.

Implementations MUST NOT require a stanza to embed a hash or URL; the artifact
table is the single place a pin is expressed, so that a version bump touches one
line.

#### 4.3 Merge with existing configuration sources

Profile-delivered stanzas MUST merge with `conformist.toml` such that
`conformist.toml` — being nearer the tree — wins on conflict, consistent with
§3. An implementation MUST report, at a diagnostic verbosity, which source
supplied each active stanza; a user debugging a rule that fires unexpectedly
MUST be able to determine where it came from.

### 5. Execution plane

Profile-delivered tools are **impure-plane only**. An implementation MUST NOT
make the sandboxed pure gate (`checks.formatting`) depend on a profile-delivered
artifact. The pure gate MUST continue to draw its tools from nixpkgs/igloo
exclusively.

This is a deliberate reduction in guarantee and MUST be documented as such to
adopters: a rule delivered by profile runs in the devshell and in pre-merge
hooks, which are per-repository configuration that can be skipped or
misconfigured, whereas a rule in the sandboxed gate is a derivation that either
builds or does not. `fetchClosure` is the identified path to restoring pure
parity and SHOULD be re-evaluated when available.

### 6. The changer lane

conformist gains a third verb alongside check and repair, for one-off
migrations: hyphence-document migration definitions, profile-delivered editing
tools, and ast-grep-like rewriters.

**The verb's name is UNRESOLVED.** Candidates under consideration are `change`,
`modify`, `edit`, and `mutate`. This document does not select one, and no
implementation should encode a choice until it is made.

A changer MUST adopt flakeclobber's refuse-on-ambiguity contract (RFC 0004): when
a target is matched more than once, or when the intended edit cannot be
distinguished from an adjacent one, the changer MUST refuse and leave the target
byte-identical rather than apply a partial or guessed edit. Existing tools
(`conform`, `flakeclobber`) are candidate first ports and are explicitly out of
scope here.

### 7. The POC as gate

The POC is a **gate on this specification**, not its first increment. This
document is `proposed`; it MUST NOT advance to `accepted` before the POC's
result and the operator's review of the resulting profile document.

In scope:

1. just-us publishes a static `just` to the fleet cache.
2. A hand-written profile in conformist's tree pinning it and carrying the
   `justfile-*` linter stanzas.
3. A resolver spike in conformist: parse profile → fetch → verify → materialize
   → merge stanzas → run check.
4. Success criterion: **conformist self-lints its own justfile**, closing the
   gap recorded in this repository's AGENTS.md.

Explicitly out of scope: papi hosting, layer walking (§3), the changer lane
(§6), and the `oci`/`drv` forms (§2).

The POC's ergonomics — not merely its exit code — determine whether the design
proceeds.

## Security Considerations

This specification moves tool acquisition from Nix evaluation, where artifacts
are built from pinned sources inside a sandbox, to **runtime download and
execution of pre-built binaries from a network location**. That is a material
change to the trust boundary and deserves to be stated plainly rather than
inherited silently.

**Hash verification is the entire trust anchor.** The `hash` field is what
distinguishes this from downloading and running an arbitrary binary.
Implementations MUST verify before execution, MUST fail closed on mismatch, and
MUST NOT provide an option to skip verification. An artifact whose hash is
absent MUST be rejected at parse time, not at fetch time.

**No trust-on-first-use.** An implementation MUST NOT record and thereafter
trust a hash it observed. Every artifact's hash MUST come from the profile.

**Delegation is remote control of local enforcement.** A locked `-` reference
lets an organization baseline determine which tools a repository downloads and
executes. Locking is what makes this safe: the reference pins an object
identity, so a compromised or changed baseline does not silently alter what runs.
An implementation MUST NOT follow an unlocked delegation (§3.1) — an unlocked
reference would let whoever serves the baseline execute arbitrary code on every
machine that lints.

**The cache is executable content on disk.** The materialization directory holds
executables outside `/nix/store` and therefore outside its immutability
guarantees. Implementations SHOULD create cache entries read-only, SHOULD name
them by content hash so a tampered entry does not satisfy a later lookup, and
MUST re-verify rather than assuming a cache hit is trustworthy.

**Profile documents are inputs, not code.** The profile is data. An
implementation MUST NOT evaluate profile content as a program, and MUST NOT
allow a stanza to escape into arbitrary host command execution beyond the
command surface RFC 0001 already defines.

**Absolute paths may reach findings.** The recipe model's envelope carries the
absolute path of the file being inspected, and linter output derived from it can
therefore contain host directory structure. Implementations and rule authors
SHOULD avoid propagating absolute paths into findings that may be published.

## Conformance Testing

Conformance tests for this specification MUST live in `zz-tests_bats/` and MUST
use binary injection via `bats-emo`, never a hardcoded build output path:

    require_bin CONFORMIST conformist

### Covered Requirements

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| §1, MUST reject an unknown type tag | `profile_parse.bats` | An unrecognized tag fails and the tag is reported |
| §2, MUST fail on hash mismatch | `profile_artifact.bats` | A tampered artifact is rejected, not executed or cached |
| §2, MUST reject a missing hash at parse time | `profile_parse.bats` | Absent `hash` fails before any fetch |
| §2.2, MUST support data artifacts | `profile_artifact.bats` | A non-executable artifact materializes and is referenceable |
| §3, closest layer wins | `profile_resolve.bats` | A nearer layer overrides one field without discarding siblings |
| §3.1, MUST NOT follow an unlocked delegation | `profile_delegate.bats` | An unlocked reference is refused |
| §3.1, MUST fail on unresolvable delegation | `profile_delegate.bats` | Failure is loud, not a silent drop of baseline rules |
| §4.3, MUST report each stanza's source | `profile_merge.bats` | Diagnostic output attributes every active stanza |

## Compatibility

This specification is additive. A repository with no profile MUST behave exactly
as it does today; profile support MUST NOT change the meaning of an existing
`conformist.toml`.

Nix-module delivery is **not** deprecated by this document. Tools available from
nixpkgs/igloo remain module-delivered and remain in the pure gate (§5). The
profile addresses the case modules cannot serve: a tool whose repository is
downstream of conformist.

Adopters currently wiring just-us's roster through a flake input are unaffected
until they migrate, and MUST NOT be required to migrate as a condition of a
conformist upgrade.

## References

### Normative

- [RFC 2119] Key words for use in RFCs to Indicate Requirement Levels
- [conformist RFC 0001] Linter Support: the `[linter.<name>]` Config Section,
  the `check` Subcommand, and Check/Repair Execution Modes
- [conformist RFC 0004] flakeclobber: Destructive flake.nix Edits for Fleet
  Migration — the refuse-on-ambiguity contract §6 generalizes

### Informative

- [eng FDR 0015] conformist profile / cache-delivered linters (umbrella)
- [just-us FDR 0003] `--dump-format model`: a normalized recipe model for policy
  consumers — the format the motivating linters consume
- conformist#85, conformist#89 — the module-qualifier and module-recursion
  defects whose fix produced the cycle this document resolves
