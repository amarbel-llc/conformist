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

*Collision preflight* (conformist#142). Into a non-empty target (*--overwrite*),
*nix flake init* would write every non-colliding file and then refuse the
colliding ones, leaving a half-applied tree and skipping the steps below. So
bootstrap first instantiates the template into a scratch directory and compares
it with the target. If any template file exists in the target with different
content, bootstrap exits 2 listing them and writes nothing; move or merge those
files and rerun. Files identical to the template are not conflicts. An empty
target skips the preflight.

After *nix flake init* the bootstrap finishes the repo-specific steps itself
(conformist#138), in this order:

- *Version key.* The literal *EXAMPLE_VERSION* the templates ship in _version.env_
  and _conformist.nix_ becomes *<REPO>\_VERSION*, derived exactly like local mode's
  _version.env_ key (the git origin remote's repo name, else the directory
  basename). *--name <x>* overrides the name. Only files that are the template's
  own are rewritten: ones it just wrote, and ones already byte-identical to the
  template (which *nix flake init* skips), so a rerun still fills placeholders.
  *--overwrite* never edits a file the user already had that differs from the
  template.
- *Crate name.* The Rust package templates' *EXAMPLE_CRATE* placeholder (in
  _Cargo.toml_ and _AGENTS.md_) becomes the same name, lower-cased and reduced to
  Cargo's alphabet, under the same rules.
- *Go name.* The Go template's *EXAMPLE_NAME* placeholder (in _go.nix_,
  _flake.nix_, _main.go_ and _AGENTS.md_: the module path's last element, the
  package name and the binary's name) becomes the same lower-cased name as the
  crate name, under the same rules.
- *Staging.* In a git repo, exactly the files the template wrote are *git add*-ed.
  Nothing is committed. Outside a git repo this is skipped and reported.
- *Locking.* *nix flake lock* runs, then _flake.lock_ is staged. It runs by default;
  *--no-lock* skips it. A failed lock, or a failed stage, prints a warning and the
  bootstrap still succeeds, since the template is already in place; the step
  is listed under the final *Remaining:* line instead. Locking is skipped when staging
  failed in a git repo, because a git flake sees only tracked files.

The closing *Remaining:* line names whatever is left (creating _.envrc_, *direnv
allow*, review and commit, plus any step that was skipped or failed).

No template ships an _.envrc_. The fleet keeps it untracked and git-ignored:
spinclass lists it in _.git/info/exclude_ and rewrites it in every worktree, so a
tracked copy makes spinclass's merge dirty check fail (conformist#141). A repo that
wants direnv creates the one-line file itself (*use flake*), as eng-direnv(7)
describes for child-repo envrc files.

# FLAKE TEMPLATES

conformist's flake exposes one template per repo language, instantiated with *nix
flake init -t <conformist flakeref>#<id>* or advertised through a domain's PAPI
document for *conform <domain>#<id>* (above). Each lives under _templates/<id>/_ and
is registered in the flake's *templates* output; a new language is one more
directory and one more entry.

- *eng*: language-agnostic. The eng preset, nixfmt, a justfile whose *default* is
  *lint*, and the sweatfile hooks. The built-in scaffold (SCAFFOLDING) tracks this
  template.
- *eng-rust*: a Rust cargo workspace. Adds rustfmt, an impure clippy lane
  (_conformist-impure.nix_, with the *gomod2nix* linter forced off since it needs a
  Go overlay a plain consumer lacks), a Rust _.gitignore_, and a justfile whose
  *default* is *test lint* with *lint-impure* in the *lint* aggregate. All three
  Rust flakes share a manpage lane: when _doc/_ exists, scdoc builds its pages into
  _packages.manpages_ (a _cli.N.scd_ or _lib.N.scd_ is installed under the crate's
  name) and _checks.manpage-names_ runs *doppelgang lint-man* (the _doppelgang_
  input; *just lint-manpage-names*); without _doc/_ it passes trivially. There is no
  dagnabit lane: it is Go-only. The version key
  in _conformist.nix_ ships as a placeholder to rename, or to drop once the root
  _Cargo.toml_ has a matching *[package]*.
- *eng-rust-bin* and *eng-rust-lib*: *eng-rust* plus a minimal Cargo package (a
  binary with _src/main.rs_, or a library with _src/lib.rs_, each with one test), a
  short _AGENTS.md_ stub, and a _CLAUDE.md_ symlink to it (*nix flake init*
  preserves the symlink; the *agents-md* linter requires a _CLAUDE.md_ symlink to
  point at _AGENTS.md_). The package name ships as the placeholder *EXAMPLE_CRATE*
  in _Cargo.toml_ and _AGENTS.md_, which bootstrap fills with the repo name
  lower-cased and reduced to Cargo's alphabet, the same name that derives the
  _version.env_ key, so *eng-versioning* needs no override and _conformist.nix_ ships
  without one.

- *eng-go*: a Go module built with godyn (godyn(7)). The *eng* and *eng-go* presets
  (goimports then gofumpt), nixfmt, an _igloo_ flake input for _buildGoAuto_, a
  _go.nix_ manifest with no dependencies (module path
  *code.linenisgreat.com/EXAMPLE_NAME*, no _go.mod_), a main package at the module
  root with one test, an example level-0 package _internal/0/buildinfo_ that
  _main.go_ uses, _packages.{default,manpages}_ and
  _checks.{formatting,tests,layout,manpage-names}_, a justfile whose *default* is
  *build test lint*, an _AGENTS.md_ stub and a _CLAUDE.md_ symlink. *layout* is
  *conformist.lib.mkLayoutCheck* (conformist-nix(7) MODULE LIBRARY) with
  purse-first's *dagnabit* (the _purse-first_ flake input) and fails when a package
  under _internal/<level>/_ sits at the wrong level; *conform* does not yet detect
  a Go repository and scaffold it, so only this template carries it;
  _doc/cli.1.scd_ is the binary's section-1 page, built by *scdoc* into
  _packages.manpages_, installed under the package's name by _packages.default_
  and checked by *manpage-names* (*doppelgang lint-man*, the _doppelgang_ input).
  Both inputs follow the template's pins, and conformist itself takes neither
  (that would be a cycle). _main.go_ imports its internal packages as
  *code.linenisgreat.com/conformist/templates/eng-go/...* --- a path that resolves
  inside conformist's own module, so its whole-module *go list* (godyn's graph)
  sees the template --- and bootstrap rewrites that prefix to the new repo's
  module path. The name ships as the placeholder *EXAMPLE_NAME* and the
  version key as *EXAMPLE_VERSION* (_conformist.nix_ sets the *eng-versioning*
  key explicitly, since go.mod is rendered inside nix). There is no bin/lib split
  as in Rust: a Go library is a module whose packages are imported, so a repo that
  only exports packages deletes _main.go_ and sets *subPackages* in _flake.nix_
  to its _cmd/_ directories, or to none.

Each template is a deliberate copy of the others (there is no shared base yet), so a
drift test keeps their language-independent files identical and checks the package
templates' placeholders and symlink.

The impure lane's *git-remotes* check requires _origin_ on the canonical host
(_code.linenisgreat.com_); see ALLOWED HOSTS for repos hosted elsewhere.

# ALLOWED HOSTS

A repo whose _origin_ is on another forge fails the impure lane's *git-remotes*
check until that host is in *linters.git-remotes.allowed-hosts* (conformist-git(7)
REMOTES). *conform* sets it up (conformist#140), in both modes:

- The _origin_ remote's host is added automatically when it is not
  _code.linenisgreat.com_.
- *--allow-host <host>* (repeatable) adds further hosts explicitly.

The hosts are persisted as one managed line in _conformist-impure.nix_:

	linters.git-remotes.allowed-hosts = [ "forge.example.org" ]; # managed by `conformist conform` (--allow-host)

It lives in _conformist-impure.nix_, not _conformist.nix_, because *git-remotes*
runs in the impure lane and only _conformist-impure.nix_ is merged into it
(_conformist.nix_ feeds the pure lane, where the option has no effect), and
because a plain Nix attribute needs no separate config format: later *conform*
runs, *conform --repair* and *just lint* all just evaluate it.

Re-runs are idempotent and only ever widen the line: a host once persisted is not
dropped by a run without the flag, even if _origin_ later moves. Only the managed
line is rewritten; the rest of the file is left alone, and the line is inserted
before the file's closing brace. If the file already sets *allowed-hosts* by hand,
or has no closing brace conform can insert before, it is left unedited and the line
to add is printed. Greenfield, the scaffold and _templates/eng_ ship a
_conformist-impure.nix_ stub that _flake.nix_ imports; when an existing flake does
not import it, the allowlist would have no effect and conform says so.

The templates pin conformist from the master tarball
(_https://code.linenisgreat.com/conformist/archive/master.tar.gz_), the pin most of
the fleet uses. The *explore-template-eng*, *explore-template-eng-rust* and
*explore-template-eng-go* recipes smoke-test instantiation; the latter two run each
template's whole default gate (*just*) in a scratch repo.

# SCAFFOLDING

*conform* writes every absent shape file: _conformist.nix_, a _version.env_ whose key
is derived from the repository name (the git origin remote, else the directory
basename, via *git.OriginRepoName*), a _sweatfile_, a _conformist-impure.nix_ overlay
stub (greenfield only, beside the _flake.nix_ that imports it), a _.gitattributes_ binding
_flake.lock_ to the *conformist-flake-lock* merge driver (required by the
*git-merge-drivers* linter, conformist-git(7) MERGE DRIVERS), and, greenfield, a
complete _flake.nix_/_justfile_. All are *//go:embed*-ed from _cmd/conform/scaffold/_
(the _.gitattributes_ source is stored there as _gitattributes_, undotted), and the
_flake.nix_/_justfile_/_sweatfile_/_.gitattributes_/_conformist-impure.nix_ are kept byte-identical to
_templates/eng/_ by a drift test (conformist#41).

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

The generated _lint-worktree_ recipe runs *nix run '.#conformist' -- check ...*, the
flake's own pinned binary, and every generated _flake.nix_ exports it as
_packages.conformist_. A bare *conformist* resolves through PATH, where eng's
home-profile wrapper can win over the devShell's and fails on a repo with no
checked-in config (conformist#131). A repo scaffolded before this carries the bare
form, and *conform* does not rewrite an existing justfile. To adopt the fix, add
_packages.conformist = conformistPkg;_ beside the other _packages.conformist-\*_
outputs (conform does not splice it into an existing flake yet) and change the
recipe's last line to the *nix run* form.

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
*conformist-justfile*(7), *conformist-testing*(7)
