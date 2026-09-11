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

A profile is a hyphence document. Its type tag MUST be
`toml-conformist_profile-v1`, carried on the `!` metadata line. Its body MUST be
TOML.

The tag follows hyphence's existing convention of naming the body format first
(`toml-blob_store_config-v3`, `toml-type-v1`), so a decoder knows how to read the
body from the tag alone. The `v1` suffix is the schema version.

An implementation MUST reject a profile whose type tag it does not recognize,
and MUST report the tag it found; it MUST NOT attempt a partial read of an
unknown version. Additive fields within a version do not change the tag.

A profile MAY delegate to another profile (§3.1).

### 2. Artifacts

The `[artifact.<name>]` table names one delivered artifact. `<name>` MUST be
unique within the merged profile.

Every artifact entry MUST declare a `form`. Three forms are defined:

| `form` | Required fields | Status |
|---|---|---|
| `static` | `url`, `markl` | REQUIRED — every implementation MUST support it |
| `oci` | `image`, `markl` | OPTIONAL escape hatch |
| `drv` | `store-path` (or a means of realizing one) | OPTIONAL escape hatch |

`static` is the primary form and the only one the POC (§7) covers.
Implementations MUST support `static`; they MAY support the others.

`markl` MUST be a purpose-full **markl-id** — `purpose@format-payload`, the
self-describing identifier wire format normative in piggy RFC 0011. It MUST NOT
be a bare SRI string. Purpose-full markl-ids are the canonical spelling for
pinned and locked references across this ecosystem, so an artifact pin, a
delegation lock (§3.1) and a signature (§3.2) are all the same kind of
identifier rather than three bespoke encodings.

A content pin SHOULD use a registered content-digest purpose whose compatible
formats include the digest in use — `dodder-blob-digest-sha256-v1` covers
`sha256` and `blake2b256`. An implementation MUST reject a `markl` whose purpose
or format it does not understand rather than fetching the artifact.

After download and before any use, the implementation MUST verify the artifact
against `markl`, and MUST fail the run with an operational error (exit 2) on
mismatch. It MUST NOT execute, cache, or partially apply an artifact that failed
verification.

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

#### 3.1 Delegation

A profile MAY delegate to an organization baseline served by papi. A delegation
MUST be verifiable in exactly one of two ways, and an implementation MUST NOT
follow one that is neither.

**Content-locked** — the reference pins the baseline's identity as a markl-id,
exactly as an artifact does (§2). The fetched baseline MUST hash to that
identity. This is immutable: the baseline cannot change under the consumer.

A content-locked delegation is spelled as hyphence's **locked field line** — a
typed edge — on a `-` metadata line:

    - baseline=<target-id> @<markl-id>

Implementations MUST NOT emit the `<` prefix for this or any other metadata
line. `<` is a deprecated synonym for `-` retained only for decoding; encoders
are required to emit `-`.

**Signature-pinned** — the reference pins one or more **signing keys**, and the
baseline carries a signature the implementation verifies (§3.2). This is
mutable by design: the baseline may be updated centrally, and consumers accept
the new content because they trust the signer rather than the bytes.

A delegation that fails to resolve or verify MUST fail the run with an
operational error. An implementation MUST NOT silently continue with the local
layers alone, because doing so would quietly drop every rule the baseline
contributes — turning a compromised or unreachable baseline into a silently
weaker lint.

Delegated layers MUST be treated as farther than every local layer, so a
repository can always override its organization baseline locally.

#### 3.2 Signature verification

A signature-pinned delegation is what makes central update possible: change the
baseline once, and every repository picks it up without editing a pin. It is
therefore the mechanism by which this design answers the requirement that
motivated it, and §3.1's content-locked form is the conservative alternative
where immutability matters more than reach.

Requirements:

- The signature and the verifying key MUST both be markl-ids. The
  `papi-doc-sig-v1` purpose (a slot-9A ECDSA P-256 signature over a PAPI
  document's canonicalized bytes) and the `piggy-piv_auth-v1` key purpose are
  the registered pair for a papi-served baseline, and an implementation
  consuming one SHOULD use them rather than registering a parallel purpose.
- The verifying key MUST be **published** by the serving domain, and MUST match
  a key the consuming profile pins. A signature by an unpinned key MUST be
  rejected even if it is otherwise valid — otherwise anyone the domain
  publishes a key for could redirect the fleet's tooling.
- A signature carried inside a hyphence profile MUST occupy its own `-` line.
  It MUST NOT be placed on the `!` type line: that arrangement was tried in the
  pigpen self-signed document and failed in practice, because the type line's
  value is split on only one delimiter and a `purpose@format-payload` markl-id
  contains more.
- Where papi's own document rules are more permissive than these, the stricter
  rule applies here. Specifically: papi treats an unsigned document as valid and
  skips a signature whose purpose it does not understand. A conformist
  implementation MUST do neither — for a signature-pinned delegation, unsigned
  MUST be rejected, and an unrecognized purpose MUST be rejected rather than
  skipped. The asymmetry is deliberate: papi is describing a person, whereas
  this document decides which binaries get executed.

Multiple pinned keys MUST be supported, so a key rotation can be performed by
co-signing with the outgoing and incoming keys before the outgoing one is
withdrawn.

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

Two mechanisms are REQUIRED, and writing a profile by hand is what showed they
were missing from the first draft:

- **Executable artifacts MUST be reachable by bare name.** A stanza's command
  invokes `just`, not a cache path, so the implementation MUST place executable
  artifacts on `PATH` for the duration of the run. Without this every command
  would have to interpolate a path for its own tool, and the stanza stops being
  readable.
- **Data artifacts MUST be interpolable by name.** A command needs the *path* of
  a non-executable artifact (`jq -f <the filter>`). An implementation MUST
  provide a reference syntax resolving an artifact name to its materialized
  path. This document does not fix the spelling; `{{artifact.<name>}}` is used
  illustratively and is NOT normative.

#### 4.4 Rule logic: inline, or as an artifact

Every `justfile-*` linter is a shell pipeline over a tool plus a **program that
is the rule itself** (for those linters, a jq filter). Implementations MUST
support BOTH ways of carrying such a program. They are not alternatives; they
answer different questions about where the rule's integrity comes from.

**Inline, on the stanza** — a dedicated non-command field carries the program
directly (TOML's `'''` literal strings hold a jq filter without escaping). The
implementation MUST pass it to the tool via a file or standard input, and MUST
NOT interpolate it into a shell command line. Its integrity is the **profile's**
integrity: the rule travels inside the document, so a signature or content lock
over the profile already covers it and no separate pin exists to get wrong.
This is the expected form for a rule authored alongside the profile.

**As a data artifact** (§2.2) — the program is fetched and pinned like any other
artifact. Required when the rule is large, shared across profiles, or produced by
a build rather than hand-written, since in those cases it does not travel in the
document and needs its own pin.

Inlining the program into `command` is REJECTED in both cases: the program would
reach the tool through a shell command line, reintroducing precisely the quoting
hazard that moving these filters into files eliminated.

A stanza MUST NOT carry a rule both ways. An implementation encountering both an
inline program and an artifact reference for the same stanza MUST fail with an
operational error rather than choose one — a silent precedence rule here would
mean a profile whose effective rule is not the one its author is reading.

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

### 6. The edit lane

conformist gains a third verb alongside check and repair, for one-off
migrations: hyphence-document migration definitions, profile-delivered editing
tools, and ast-grep-like rewriters.

The verb is **`edit`** (`conformist edit`), chosen over `change`, `modify` and
`mutate`. It sits alongside the existing `check` and the bare repair command.

`edit` MUST adopt flakeclobber's refuse-on-ambiguity contract (RFC 0004): when a
target is matched more than once, or when the intended edit cannot be
distinguished from an adjacent one, it MUST refuse and leave the target
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

Explicitly out of scope for v1: papi hosting, layer walking (§3), delegation and
signature verification (§3.1, §3.2 — the v1 profile is local and hand-written, so
it has no baseline to delegate to), the edit lane (§6), and the `oci`/`drv`
forms (§2).

Deferring signatures does not weaken v1: it exercises the artifact pin, which is
the mechanism every other part depends on, and it answers the ergonomics question
fastest. It does mean v1 cannot demonstrate central update, so that property is
**claimed but unproven** at the end of v1 and MUST NOT be treated as validated by
a green v1.

### 7.1 POC v2 — required before production

Because central update is the property that motivates the whole design, it MUST
be demonstrated before this specification is relied on in production. A second
POC increment is therefore REQUIRED, not optional:

1. A signed baseline profile, served and signature-pinned per §3.2.
2. A consumer that pins the signing key and verifies it.
3. Success criterion: changing the baseline **once** changes what a consuming
   repository runs, with no edit to that repository.

This specification MUST NOT advance beyond `experimental` until v2 succeeds. A
green v1 authorizes continued development; it does not authorize production
adoption, because the benefit that justifies the design would still be untested.

Splitting it this way keeps the first gate small enough to answer the ergonomics
question quickly, while making it impossible to reach production having only ever
proven the easy half.

The POC's ergonomics — not merely its exit code — determine whether the design
proceeds.

## Security Considerations

This specification moves tool acquisition from Nix evaluation, where artifacts
are built from pinned sources inside a sandbox, to **runtime download and
execution of pre-built binaries from a network location**. That is a material
change to the trust boundary and deserves to be stated plainly rather than
inherited silently.

**Verification is the entire trust anchor.** The `markl` pin is what
distinguishes this from downloading and running an arbitrary binary.
Implementations MUST verify before execution, MUST fail closed on mismatch, and
MUST NOT provide an option to skip verification. An artifact whose `markl` is
absent, or whose purpose/format is unrecognized, MUST be rejected at parse time,
not at fetch time.

**No trust-on-first-use.** An implementation MUST NOT record and thereafter
trust an identity it observed. Every artifact's pin MUST come from the profile.

**Sign the policy; pin the artifacts.** These are different trust questions and
this document answers them differently on purpose. A signature answers *who
decided this* and permits central change; a content pin answers *are these the
exact bytes they meant* and forbids change. A baseline update therefore alters
**pinned artifact identities inside a signed document**: the fleet gets one
authoritative change, and every executed byte remains individually verified. An
implementation MUST NOT accept an artifact solely because the document naming it
was signed — a signature over a document is not a signature over its
dependencies.

**Delegation is remote control of local enforcement, and signature-pinning makes
that explicit.** A delegation decides which tools a repository downloads and
executes. Under signature-pinning the operator of the pinned key can change what
every consuming machine runs, by design — that is the central-update property,
and it is also the blast radius. It is bounded by three things and no others:
the key is hardware-resident (a PIV slot-9A key, so signing requires the
physical card), the key MUST be both published by the domain and pinned by the
consumer (§3.2), and the artifacts it names remain individually pinned.

**Revocation is the expensive direction, and MUST be planned for.** Rotation is
cheap: co-sign with the outgoing and incoming keys. Revocation is not — a
compromised or lost key must be un-pinned in every consuming profile, which is
precisely the fleet-wide sweep this design exists to avoid. Implementations
SHOULD make the set of pinned keys easy to enumerate across a fleet so that a
revocation sweep is mechanical. Deployments SHOULD prefer content-locked
delegation (§3.1) where the baseline is not expected to change, since it has no
revocation problem at all.

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
| §2, MUST fail on pin mismatch | `profile_artifact.bats` | A tampered artifact is rejected, not executed or cached |
| §2, MUST reject an absent or unrecognized `markl` at parse time | `profile_parse.bats` | Failure precedes any fetch |
| §2.2, MUST support data artifacts | `profile_artifact.bats` | A non-executable artifact materializes and is referenceable |
| §3, closest layer wins | `profile_resolve.bats` | A nearer layer overrides one field without discarding siblings |
| §3.1, MUST NOT follow an unverifiable delegation | `profile_delegate.bats` | Neither content-locked nor signature-pinned is refused |
| §3.1, MUST fail on unresolvable delegation | `profile_delegate.bats` | Failure is loud, not a silent drop of baseline rules |
| §3.2, MUST reject a signature by an unpinned key | `profile_signature.bats` | A validly-signed baseline signed by a published-but-unpinned key is refused |
| §3.2, MUST reject unsigned and unrecognized-purpose | `profile_signature.bats` | papi's permissive defaults are not inherited |
| §3.2, MUST accept co-signed rotation | `profile_signature.bats` | Outgoing plus incoming key verifies |
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
- [piggy RFC 0011] markl-id wire format — normative for the `purpose@format-payload`
  grammar, the blech32 encoding, and the cross-language stable purpose registry
  used by §2 pins, §3.1 locks and §3.2 signatures. Note that madder's
  `docs/rfcs/0002-markl-id-format.md` is a **superseded stub** and MUST NOT be
  cited as normative, notwithstanding references to it elsewhere in the fleet;
  `markl-id(7)` is the readable summary.
- [conformist RFC 0001] Linter Support: the `[linter.<name>]` Config Section,
  the `check` Subcommand, and Check/Repair Execution Modes
- [conformist RFC 0004] flakeclobber: Destructive flake.nix Edits for Fleet
  Migration — the refuse-on-ambiguity contract §6 generalizes
- [papi RFC-0001] Personal API wire format — §10 (Document Signature) for the
  `papi-doc-sig-v1`/`piggy-piv_auth-v1` pair, key publication, and the
  canonicalized signing input §3.2 builds on

### Informative

- [eng FDR 0015] conformist profile / cache-delivered linters (umbrella)
- [just-us FDR 0003] `--dump-format model`: a normalized recipe model for policy
  consumers — the format the motivating linters consume
- conformist#85, conformist#89 — the module-qualifier and module-recursion
  defects whose fix produced the cycle this document resolves
