// Package flakeedit performs targeted in-place surgery on a flake.nix to
// wire it into conformist: it splices the `conformist` and `just-us`
// inputs, their outputs arguments, the per-system `let` bindings
// (conformistPkg/justPkg/eval/impureEval), and the per-system output
// attributes (formatter/checks.formatting/packages.conformist-*/
// devShells.default), preserving the rest of the file byte-for-byte.
//
// It recognizes exactly one flake shape — `outputs = { … }:
// utils.lib.eachDefaultSystem (system: let … in { … })` — which the
// conformist survey found to cover 95% of eng repos. Any other shape
// (flake-parts, a raw forAllSystems/genAttrs flake, an `eachSystem`
// variant, a foreign repo that already binds `eval`/`impureEval` to
// something else) yields ErrUnrecognized, and the caller falls back to
// printing the wiring for the user to paste rather than risk corrupting
// the file.
//
// The narrow roster is deliberate: widening it to the eng-style hybrid
// (`<expr> // eachDefaultSystem (…)` with a non-attrset per-system body)
// or igloo's raw forAllSystems/genAttrs shape would require parsing
// arbitrary Nix expressions, abandoning the byte-faithful shallow-PEG
// approach and adding mis-edit risk to the common path; it is tracked
// separately (conformist#65) and only worth doing when a repo we want to
// conform lands outside this shape.
//
// The shallow Nix PEG approach is modelled on amarbel-llc/doppelgang's
// internal/0/nixedit (langlang runtime); doppelgang edits only `inputs`,
// flakeedit extends it to the four splice targets above.
package flakeedit

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	flakeparse "code.linenisgreat.com/conformist/cmd/conform/flakeparse"
)

// ErrUnrecognized is re-exported from flakeparse so callers that imported
// this package directly continue to work.
var ErrUnrecognized = flakeparse.ErrUnrecognized

// EditReport summarizes what Apply changed, so the caller can report it
// and decide an exit code.
type EditReport struct {
	// Added lists the pieces spliced in (e.g. "input conformist",
	// "formatter", "let bindings").
	Added []string
	// Conflicts lists output attributes left untouched because a
	// different definition already exists (e.g. "formatter"); the caller
	// prints these for the user to reconcile by hand.
	Conflicts []string
}

// Changed reports whether Apply modified the source.
func (r EditReport) Changed() bool { return len(r.Added) > 0 }

// Options tunes Apply.
type Options struct {
	// ForceFormatter replaces an existing `formatter` attribute's value with
	// conformist's wrapper instead of leaving it a conflict (#63). Off by
	// default: a repo's own formatter is preserved unless the caller opts in.
	ForceFormatter bool
}

// The two top-level flake inputs conform wires in.
const (
	conformistInput = "conformist"
	justUsInput     = "just-us"

	// conformistPkgName is the let binding every conformist-wired flake has
	// carried since the first version — the sentinel that distinguishes
	// conformist's own wiring from a foreign name collision.
	conformistPkgName = "conformistPkg"
)

// conformistDeps are conformist's own flake inputs, in the order their
// follows lines are rendered.
var conformistDeps = []string{"igloo", "nixpkgs-master", "utils"}

// justUsFollows are the follows wired INSIDE the just-us input, in render
// order: dep is just-us's own input name, target the consumer's top-level
// input it follows.
var justUsFollows = []struct{ dep, target string }{
	{"nixpkgs", "nixpkgs"},
	{"flake-utils", "utils"},
	{conformistInput, conformistInput},
}

// letBinding is one per-system `let` binding conform wires in. The bindings
// are rendered INDIVIDUALLY rather than as one opaque block so Apply can
// splice in just the ones a flake is missing — which is what lets it upgrade
// a repo wired by an older conformist (conformist#100).
type letBinding struct {
	name string
	text func(indent string) string
}

// legacyLetNames is the binding set EVERY conformist has written, back to the
// first version that wired flakes at all. A flake carrying all of these but
// not the full roster is not a foreign name collision — it is conformist's own
// outdated wiring, and Apply upgrades it in place instead of refusing
// (conformist#100). A flake missing any of these while binding some of the
// rest is a genuine collision and still falls back to print-only.
var legacyLetNames = []string{conformistPkgName, "eval", "impureEval"}

// conformistLetNames are the let bindings conform wires in, keyed by name
// for idempotency.
var conformistLetNames = func() []string {
	bindings := conformistLetBindings()
	names := make([]string, 0, len(bindings))

	for _, b := range bindings {
		names = append(names, b.name)
	}

	return names
}()

// Apply splices conformist's wiring into src and returns the rewritten
// source plus a report of what changed. It returns ErrUnrecognized (and
// src unchanged) when the flake is not the recognized shape; the caller
// then falls back to print-only.
func Apply(src []byte, opts Options) ([]byte, EditReport, error) {
	ins, outs, err := flakeparse.ParseFlake(src)
	if err != nil {
		if errors.Is(err, flakeparse.ErrUnrecognized) {
			return src, EditReport{}, ErrUnrecognized
		}

		return src, EditReport{}, fmt.Errorf("flakeedit: %w", err)
	}

	// Idempotency sentinel. A fully-wired flake has every let binding; a
	// clean flake has none. Partial presence used to be refused outright as a
	// foreign collision — but that refused exactly the repos the fleet
	// migration targets: those wired by a PREVIOUS conformist, before
	// `justPkg` joined the roster (conformist#100). Discriminate instead:
	// when every binding conformist has ALWAYS written is present, the file
	// is conformist's own outdated wiring and is safe to upgrade in place.
	// Any other partial state is still a foreign collision → print-only.
	have := 0
	for _, n := range conformistLetNames {
		if outs.LetExisting[n] {
			have++
		}
	}

	alreadyWired := have == len(conformistLetNames)
	outdatedWiring := false

	if !alreadyWired && have > 0 {
		for _, n := range legacyLetNames {
			if !outs.LetExisting[n] {
				return src, EditReport{}, ErrUnrecognized
			}
		}

		outdatedWiring = true
	}

	// Any non-zero count means the existing output attrs are conformist's own
	// (the partial case that is NOT ours already returned above), so they are
	// not reported as conflicts to reconcile.
	conformistWired := have > 0

	var (
		report  EditReport
		splices []flakeparse.Splice
	)

	// 1. inputs
	if s, labels, ok := inputsSplice(ins); ok {
		splices = append(splices, s)
		report.Added = append(report.Added, labels...)
	}

	// 2. outputs arguments
	if s, labels, ok := argSplice(outs); ok {
		splices = append(splices, s)
		report.Added = append(report.Added, labels...)
	}

	// 3. let bindings — only the ones missing, so an outdated-wiring flake
	// gains just `justPkg` rather than a duplicate block.
	if s, names, ok := letSplice(src, outs); ok {
		splices = append(splices, s)
		report.Added = append(report.Added, "let bindings ("+strings.Join(names, ", ")+")")
	}

	// An existing `eval` binding is an opaque value this shallow parser will
	// not rewrite, so a repo upgraded from older wiring keeps its old eval
	// body — which predates the justfile-orphan-summary linter. Adding
	// `justPkg` alone does not wire that linter up, so say so rather than
	// let the operator assume the upgrade was complete.
	if outdatedWiring {
		report.Conflicts = append(report.Conflicts,
			"eval (existing binding left as-is; add the "+
				"justfile-orphan-summary import + linters.justfile-orphan-summary "+
				"settings by hand)")
	}

	// 4. return attributes
	retS, added, conflicts := returnSplice(src, outs, conformistWired, opts.ForceFormatter)
	if retS.Text != "" {
		splices = append(splices, retS)
	}

	report.Added = append(report.Added, added...)
	report.Conflicts = append(report.Conflicts, conflicts...)

	// 5. devShell packages-list merge (#63). Skipped when a trailing `//`
	// merge redefines devShells: the list we would edit is the shadowed one.
	if outs.DevShellPackages != nil && !outs.MergeShadows("devShells.default") {
		if s, ok := devShellMergeSplice(src, *outs.DevShellPackages); ok {
			splices = append(splices, s)
			report.Added = append(report.Added, "devShells.default packages")
		}
	}

	// 6. forced formatter replacement (#63)
	if opts.ForceFormatter && outs.FormatterValue != nil {
		const wrapper = "eval.config.build.wrapper"
		if strings.TrimSpace(string(src[outs.FormatterValue.Start:outs.FormatterValue.End])) != wrapper {
			splices = append(splices, flakeparse.Splice{
				Offset: outs.FormatterValue.Start,
				End:    outs.FormatterValue.End,
				Text:   wrapper,
			})
			report.Added = append(report.Added, "formatter (replaced)")
		}
	}

	if len(splices) == 0 {
		return src, report, nil
	}

	// Apply highest offset first so earlier offsets stay valid.
	sort.Slice(splices, func(i, j int) bool { return splices[i].Offset > splices[j].Offset })
	out := src
	for _, s := range splices {
		out = s.ApplyTo(out)
	}

	return out, report, nil
}

// inputsSplice builds the insertion for the conformist input bindings not
// already present, honoring block vs flat form.
//
// conformist#83: conform must never introduce a top-level input the
// consumer's outputs pattern does not already name — a strict
// (no-`...`) destructuring then fails eval. Each of conformist's own
// inputs that the consumer also has is deduped from INSIDE the conformist
// input, and no top-level nixpkgs is ever added. The just-us input
// follows the same rule from the other direction: only existing top-level
// inputs are followed.
func inputsSplice(ins flakeparse.InputsAttrSet) (flakeparse.Splice, []string, bool) {
	present := ins.TopLevelNames()

	var lines, labels []string

	if !present[conformistInput] {
		lines = append(lines, `conformist.url = "git+https://code.linenisgreat.com/conformist.git";`)

		for _, dep := range conformistDeps {
			if present[dep] {
				lines = append(lines, fmt.Sprintf("conformist.inputs.%s.follows = %q;", dep, dep))
			}
		}

		labels = append(labels, "input conformist")
	}

	if !present["utils"] {
		lines = append(lines, `utils.follows = "conformist/utils";`)
		labels = append(labels, "input utils")
	}

	if !present[justUsInput] {
		lines = append(lines, `just-us.url = "git+https://code.linenisgreat.com/just-us.git";`)

		willExist := maps.Clone(present)
		willExist[conformistInput] = true
		willExist["utils"] = true

		for _, f := range justUsFollows {
			if !willExist[f.target] {
				continue
			}

			lines = append(lines, fmt.Sprintf("just-us.inputs.%s.follows = %q;", f.dep, f.target))
		}

		labels = append(labels, "input just-us")
	}

	if len(labels) == 0 {
		return flakeparse.Splice{}, nil, false
	}

	var b strings.Builder

	for _, text := range lines {
		if !ins.BlockMode {
			text = "inputs." + text
		}
		if ins.LeadNewline {
			b.WriteString("\n")
			b.WriteString(ins.Indent)
			b.WriteString(text)
		} else {
			b.WriteString(ins.Indent)
			b.WriteString(text)
			b.WriteString("\n")
		}
	}
	if ins.LeadNewline && ins.TrailNewlineIndent != "" {
		b.WriteString("\n")
		b.WriteString(ins.TrailNewlineIndent)
	}

	return flakeparse.Splice{Offset: ins.InsertOffset, Text: b.String()}, labels, true
}

// argNames are the inputs the spliced per-system body references by name.
var argNames = []string{conformistInput, justUsInput}

// argSplice builds the single insertion adding the missing outputs-pattern
// arguments. One splice rather than one per name: equal-offset splices
// have no defined order under Apply's sort, so rendering them together
// keeps the result deterministic.
func argSplice(outs flakeparse.ParsedOutputs) (flakeparse.Splice, []string, bool) {
	var (
		b      strings.Builder
		labels []string
	)

	for _, name := range argNames {
		if outs.ArgNames[name] {
			continue
		}

		b.WriteString("\n" + outs.ArgIndent + name + ",")
		labels = append(labels, "outputs arg "+name)
	}

	if len(labels) == 0 {
		return flakeparse.Splice{}, nil, false
	}

	return flakeparse.Splice{Offset: outs.ArgInsertOff, Text: b.String()}, labels, true
}

// conformistLetBindings returns the per-system `let` bindings conform wires
// in, in render order.
func conformistLetBindings() []letBinding {
	return []letBinding{
		{conformistPkgName, func(i string) string {
			return i + conformistPkgName + " = conformist.packages.${system}.default;\n"
		}},
		{"justPkg", func(i string) string {
			return i + "justPkg = just-us.packages.${system}.default;\n"
		}},
		// The justfile-* convention linters ship from just-us, not conformist:
		// they read the fork-only `just --dump --dump-format model`, and
		// conformist must stay strictly upstream of just-us (which already
		// inputs it). The roster enables all eight — the seven conventions plus
		// justfile-orphan-summary — off ONE shared justPackage, so this splices
		// the roster rather than the single orphan-summary module it used to.
		{"eval", func(i string) string {
			return "" +
				i + "eval = conformist.lib.evalModule pkgs {\n" +
				i + "  imports = [\n" +
				i + "    conformist.lib.presets.eng\n" +
				i + "    just-us.lib.conformistPresets.justfile\n" +
				i + "    ./conformist.nix\n" +
				i + "  ];\n" +
				i + "  package = conformistPkg;\n" +
				"\n" +
				i + "  linters.justfile-common.justPackage = justPkg;\n" +
				i + "};\n"
		}},
		{"impureEval", func(i string) string {
			return "" +
				i + "impureEval = conformist.lib.evalModule pkgs {\n" +
				i + "  imports = [ conformist.lib.presets.eng-impure ];\n" +
				i + "  package = conformistPkg;\n" +
				i + "  projectRootFile = \"flake.nix\";\n" +
				i + "};\n"
		}},
	}
}

// letSplice builds the insertion for whichever conformist let bindings are
// NOT already present, placed just before `in`, and names the ones it added.
// On a clean flake that is all four; on a flake carrying an older
// conformist's wiring it is only the ones added since (conformist#100).
func letSplice(src []byte, outs flakeparse.ParsedOutputs) (flakeparse.Splice, []string, bool) {
	i := outs.LetIndent

	var parts, names []string

	for _, b := range conformistLetBindings() {
		if outs.LetExisting[b.name] {
			continue
		}

		parts = append(parts, b.text(i))
		names = append(names, b.name)
	}

	if len(parts) == 0 {
		return flakeparse.Splice{}, nil, false
	}

	// Each part already ends in "\n"; joining on "\n" reproduces the blank
	// line between bindings.
	return beforeCloser(src, outs.LetCloseOff, "\n"+strings.Join(parts, "\n")), names, true
}

// returnAttr is one output attribute conform wires in.
type returnAttr struct {
	path string
	text func(indent string) string
}

func returnAttrs() []returnAttr {
	return []returnAttr{
		{"formatter", func(i string) string {
			return i + "formatter = eval.config.build.wrapper;\n"
		}},
		{"checks.formatting", func(i string) string {
			return i + "checks.formatting = eval.config.build.check self;\n"
		}},
		{"packages.conformist-impure-config", func(i string) string {
			return i + "packages.conformist-impure-config = impureEval.config.build.configFile;\n"
		}},
		{"packages.conformist-pre-commit", func(i string) string {
			return i + "packages.conformist-pre-commit = eval.config.build.preCommit;\n"
		}},
		{"packages.conformist-repair", func(i string) string {
			return i + "packages.conformist-repair = eval.config.build.repair;\n"
		}},
		{"devShells.default", func(i string) string {
			return "" +
				i + "devShells.default = pkgs.mkShell {\n" +
				i + "  packages = [\n" +
				i + "    conformistPkg\n" +
				i + "    eval.config.build.preCommit\n" +
				i + "    eval.config.build.repair\n" +
				i + "    justPkg\n" +
				i + "  ];\n" +
				i + "};\n"
		}},
	}
}

// returnSplice builds the insertion for the output attributes not already
// present.
func returnSplice(
	src []byte,
	outs flakeparse.ParsedOutputs,
	conformistWired, forceFormatter bool,
) (flakeparse.Splice, []string, []string) {
	i := outs.RetIndent

	var (
		body      strings.Builder
		added     []string
		conflicts []string
	)
	for _, a := range returnAttrs() {
		// An attr defined on a TRAILING `//` merge side overrides the same attr
		// in the per-system body (conformist#65). Splicing it anyway would
		// produce wiring that is silently dead — a conform run reporting
		// success while changing nothing observable. Report it instead.
		if outs.MergeShadows(a.path) {
			conflicts = append(conflicts,
				a.path+" (defined on the trailing // merge side, which would override it)")

			continue
		}

		if outs.RetExisting[a.path] {
			if a.path == "devShells.default" && outs.DevShellPackages != nil {
				continue
			}

			if a.path == "formatter" && forceFormatter && outs.FormatterValue != nil {
				continue
			}

			if !conformistWired {
				conflicts = append(conflicts, a.path)
			}

			continue
		}

		if parent, ok := dottedParent(a.path); ok && outs.RetExisting[parent] {
			if !conformistWired {
				conflicts = append(conflicts, a.path)
			}

			continue
		}

		body.WriteString(a.text(i))
		added = append(added, a.path)
	}
	if body.Len() == 0 {
		return flakeparse.Splice{}, added, conflicts
	}

	return beforeCloser(src, outs.RetCloseOff, "\n"+body.String()), added, conflicts
}

// dottedParent returns the parent attr-path of a dotted path.
func dottedParent(path string) (string, bool) {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i], true
	}

	return "", false
}

// devShellPackages are the tools conform merges into an existing
// devShells.default packages list (#63), in order.
var devShellPackages = []string{
	conformistPkgName,
	"eval.config.build.preCommit",
	"eval.config.build.repair",
	"justPkg",
}

// devShellMergeSplice builds the insertion that adds conformist's tools
// to an existing devShells.default packages list.
func devShellMergeSplice(src []byte, ls flakeparse.ListSplice) (flakeparse.Splice, bool) {
	indent := flakeparse.LineIndent(src, ls.CloseOff) + "  "

	var body strings.Builder

	for _, pkg := range devShellPackages {
		if flakeparse.TokenIndex(ls.Inner, pkg) >= 0 {
			continue
		}

		body.WriteString(indent + pkg + "\n")
	}

	if body.Len() == 0 {
		return flakeparse.Splice{}, false
	}

	return beforeCloser(src, ls.CloseOff, body.String()), true
}

// beforeCloser builds a splice that inserts body just before a closer
// token (a `}` or the `in` keyword) at closeOff.
func beforeCloser(src []byte, closeOff int, body string) flakeparse.Splice {
	if flakeparse.OnlyBlankBefore(src, closeOff) {
		return flakeparse.Splice{Offset: flakeparse.LineStart(src, closeOff), Text: body}
	}

	closerIndent := flakeparse.LineIndent(src, closeOff)

	return flakeparse.Splice{Offset: closeOff, Text: "\n" + body + closerIndent}
}
