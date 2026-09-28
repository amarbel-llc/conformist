conformist-conform(7)

# NAME

conformist-conform - how conform and flakeclobber rewrite a flake.nix

# DESCRIPTION

*conformist conform* scaffolds a repository into the eng shape, and *flakeclobber*
sweeps a targeted edit across a fleet of them. Both do something unusual for
conformist: they rewrite a _flake.nix_ the operator wrote, in place. This page is
the reference for that machinery --- which shapes are recognized, which are refused
and why, how a splice is placed, and what each tool guarantees.

See *conformist-conform*(1) for the command's own flags and modes, and
*conformist-nix*(7) for the flake conventions the linters enforce. The design
records are _docs/rfcs/0004-flakeclobber.md_ and conformist#17/#42/#43.

A note on why any of this is careful. Editing someone's _flake.nix_ has a failure
mode worse than refusing to: a rewrite that parses but means something different.
Every rule below exists because the alternative was a silent wrong edit, so the
standing bias is to refuse a shape rather than guess at it. "We could splice it" is
not "it is safe to".

# THE TWO-PASS PARSER

The shared PEG infrastructure lives in _cmd/conform/flakeparse/_ (modelled on
amarbel-llc/doppelgang's *nixedit*): three grammars, navigation helpers, splice
types, and *ParseFlake*. _cmd/conform/flakeedit/_ imports it for the wiring-specific
logic, and *flakeclobber* imports it for the sweep.

Parsing runs in two passes over three grammar files:

*nix.peg*
	The FIRST pass. Locates the byte span of the *outputs* value and treats
	everything else as opaque.

*outputs.peg*
	The SECOND pass. Parses the recognized shape within that span.

*shared.peg*
	The lexical layer both passes pull in by name, via langlang's
	*@import <names> from "./shared.peg"*. Dependencies resolve transitively, so
	an importer lists only what it references directly, and *compileMatcher*
	registers the shared file with the loader alongside the entrypoint.
	_shared.peg_ is never an entrypoint and defines no *File* rule.

## The shared layer is enforced, not advisory

Those lexical rules were once COPIED into both grammars under a "self-contained"
policy that nothing enforced, and they diverged. Two fixes landed in _outputs.peg_
only, leaving the first pass carrying both defects (conformist#106):

- the atomic-word fix, which consumes identifiers whole so keyword lookaheads are
  tested only at token boundaries. Without it *bin*, *plugin*, *origin* and
  *writeShellScriptBin* end a *let* run mid-word.
- the *with <expr>;* construct.

A disagreement between the passes about where a value ENDS is the worst bug class
available to a tool that rewrites _flake.nix_ in place, so prefer adding a rule to
_shared.peg_ over reintroducing a local copy. That preference is *enforced*:
*compileMatcher* refuses an entrypoint that locally defines any name _shared.peg_
defines.

This is not paranoia about a hypothetical. langlang resolves such a collision
silently in favour of the LOCAL rule --- it parses the importing file first, and
*GrammarNode.AddDefinition* is a no-op when the name is already taken
(_langlang/go@v0.0.12_, _grammar_ast.go:732_) --- so a re-added copy would shadow
its way straight back to conformist#106 with no diagnostic at all. Reported
upstream as langlang#30.

# RECOGNIZED SHAPE

The roster is an *eachDefaultSystem* application, plus the following variations. Each
was added because refusing it was a parser limitation rather than a deliberate
roster choice.

*A redundant paren* wrapping the whole application
	_(utils.lib.eachDefaultSystem (…))_. In Nix that paren is identity
	(conformist#101).

*A top-level* *with <expr>;* *in a binding value*
	conformist#103. Its _;_ belongs to the *with*, not to the binding --- the same
	defect class as a nested _let … in_.

*The eng-hybrid* *//* *merge, in either spelling*
	The merge attrset may lead (_{ mods } // each (…)_, circus) or trail
	(_each (…) // { mods }_, just-us/piggy), and it composes with the paren
	(dodder and piggy are both). Across the fleet the merge side carries only
	system-independent outputs --- *nixosModules*, *homeManagerModules*, *lib* ---
	so the per-system attrset is the unambiguous splice target (conformist#65).

*An at-pattern argument, in either spelling*
	_{ … }@inputs:_ or _inputs@{ … }:_ (conformist#104). The bound name is added to
	*ArgNames*, because Nix rejects a formal that collides with it.

*A chained* *let … in let … in* *per-system body*
	Sibling blocks, distinct from the nested-in-a-value case (conformist#105).
	*LetExisting* is the UNION across blocks, so the idempotency sentinel cannot
	misfire, and the splice point is the LAST *in*, so a new binding can reference
	anything bound earlier.

# REFUSED SHAPES

Refused deliberately, not for want of parsing:

- flake-parts.
- raw *forAllSystems* / *genAttrs*.
- the *eachSystem* variant.
- a *rec { … }* per-system return. This one is structurally splicable, and is
  refused anyway: *rec* puts every sibling attr in scope, so an inserted attr could
  be captured by, or shadow, a name the repository already binds.

# SHADOWED TARGETS

*//* is a *shallow* update giving its right operand precedence. So a TRAILING merge
that redefines a top-level attr conformist wires --- *formatter*, *checks*,
*packages*, *devShells* --- would silently override wiring spliced into the
per-system body, leaving dead wiring behind.

*ParsedOutputs.MergeShadows* compares root segments so callers can act on it:
*flakeedit* reports a conflict, and *flakeclobber* refuses with *ErrShadowedTarget*.
Neither writes wiring it knows would be overridden.

# SPLICING

*flakeedit* splices by byte offset, so the rest of the file is preserved verbatim.
It is per-target idempotent and never clobbers an output attr it did not write.

*Partial let-binding state is discriminated, not refused outright* (conformist#100).
A flake carrying only SOME of the *conformistPkg* / *justPkg* / *eval* / *impureEval*
bindings is read two ways. When every binding conformist has always written
(*conformistPkg*, *eval*, *impureEval*) is present, this is conformist's own
*outdated wiring*: the missing bindings are spliced in individually. That is exactly
the fleet-migration population, which an earlier blanket refusal blocked. Any other
partial state is treated as a foreign name collision and falls back to print-only.

A pre-existing *eval* body is opaque to this parser and is left alone, reported as a
conflict.

*Pre-existing outputs.* An existing *devShells.default* is merged into ---
conformist's tools are spliced into its *packages* list. An existing *formatter* is
replaced only under *--force-formatter*. Any other pre-existing attr is reported as
a conflict to reconcile by hand (conformist#63).

*Conformance sentinels.* A _flake.nix_ or _justfile_ that ALREADY carries the wiring
is detected and left silent, rather than nagging with a paste snippet: a justfile
*lint-fmt* recipe or _checks.${system}.formatting_, and a flake referencing
*conformist.lib.evalModule* (conformist#42).

# PAPI TEMPLATE MODE

*conform <domain>* and *conform <domain>#<id>* are a distinct mode (conformist#43):
instead of scaffolding from the embedded files, they resolve a flake template that
the domain advertises through its PAPI document, surface the resolved *flakeref*, and
run *nix flake init -t <flakeref>*.

Resolution (_cmd/conform/papi/_) fetches _https://<domain>/.well-known/papi_, follows
*resources.templates* within the operator's own DNS tree, and reads the visible
*templates[]* of the _{data,meta}_ collection per PAPI RFC-0001 §7/§8.

A bare domain advertising one template uses it. Several prompt via *huh* on a TTY,
and otherwise fail listing the ids rather than guessing. Operational failures map to
exit 2 (*ErrConformFailed*), and a non-empty target is refused without *--overwrite*.

# SCAFFOLDING

*conform* writes every absent shape file: _conformist.nix_, a _version.env_ whose key
is derived from the repository name (the git origin remote, else the directory
basename, via *git.OriginRepoName*), and, greenfield, a complete
_flake.nix_/_justfile_. All are *//go:embed*-ed from _cmd/conform/scaffold/_, and the
_flake.nix_/_justfile_ are kept byte-identical to _templates/eng/_ by a drift test
(conformist#41).

An existing _flake.nix_ in the recognized shape is edited in place to splice the
*conformist* and *just-us* inputs --- the latter supplying both the devShell's *just*
and the *justfile-orphan-summary* linter module --- and the per-system outputs wiring
(conformist#61). Any other shape, or *--no-edit*, falls back to printing the wiring
to paste. An existing justfile is never edited; its recipes are printed.

To finish converging a brownfield tree, *conform* prints a single *RepairCommand*:
*nix fmt* (pure formatter and file-linter repair) followed by the eng-impure lane's
linters (*agents-md*, *gomod2nix*, …) in repair mode over the working tree. That
delegates the real content edits to conformist's own linters. *conform --repair* runs
that SAME command inline --- working tree only, no commit, leaving changes for the
operator to review, which is the adoption-wave zero-action path. The emitted and the
executed forms are one string (conformist#42).

*conform* is idempotent overall: it exits 3 when it wrote, edited or repaired files
(*ErrScaffolded*), and 0 when the tree is already conformant.

# FLAKECLOBBER

*flakeclobber* is a separate binary for fleet migration (RFC 0004,
conformist#99/#100). It applies targeted list-element replacements in
*devShells.default.packages* across many repositories --- for example *pkgs.just* to
*justPkg* --- sharing the parser above.

It is dry-run by default; *--apply* writes. Each rewrite is verified with
*nix-instantiate --parse* before anything reaches disk, passing *-* and NOT
_/dev/stdin_: Go's os.Pipe-backed stdin makes nix reject the latter as a nonexistent
path, which would silently reduce every *--apply* to a no-op.

*--old* and *--new* must be paired one-for-one, and deleting takes an explicit
*--new ""* so that a missing flag can never become an implicit deletion.

Writes are *two-phase*: every rewrite is computed and parse-gated first, and a
parse-gate failure writes nothing at all. A per-file shape refusal fails only that
file.

It refuses, leaving _src_ untouched, on an unrecognized shape, no devShell, partial
state, an element appearing twice (a single-span splice would half-apply), and a
replacement identifier that is *not bound* in the flake. That last is the
conformist#100 guard: *--parse* is syntax-only and will happily accept a flake
referencing an undefined variable.

Exit codes: 0 on success, including already-migrated and not-applicable; 1 on a
migration or parse-gate failure; 2 on an operational error.

Do NOT wire it into *conform*. It is an intentionally separate one-shot sweep tool.

## Sweep order matters

The additive half of a migration is *conform*'s job --- the *just-us* input, the
*justPkg* let binding --- and the destructive half is flakeclobber's. Run
destructive-first and the result references an unbound *justPkg*, which is what the
binding guard above catches.

Because *conform* merges *justPkg* into the devShell list while *pkgs.just* is still
present, the correct post-*conform* operation is a *deletion*
(*--old pkgs.just --new ""*), not a replacement. A replacement then sees both and
refuses as ambiguous.

# VERIFICATION

Both rewriters are gated on real Nix parsing, in the *verify* lane, so a splice
regression that yields unparseable Nix fails CI:

*just verify-flakeedit-parse*
	Runs *conform* over the _test/flakeedit/_ fixtures and
	*nix-instantiate --parse*s each rewrite.

*just verify-flakeclobber-parse*
	The same for _test/flakeclobber/_.

A refusal-roster test needs a *positive control* asserting that the wrapper itself
parses. Without one, every case can be refusing for a trivial shared reason ---
which is what happened once, when no case in the roster had an *inputs* block and
*ParseFlake* requires one, so none of them exercised the shape it claimed to pin.

# SEE ALSO

*conformist*(7), *conformist-conform*(1), *conformist-nix*(7),
*conformist-justfile*(7)
