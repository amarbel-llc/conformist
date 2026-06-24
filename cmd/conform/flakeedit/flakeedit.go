// Package flakeedit performs targeted in-place surgery on a flake.nix to
// wire it into conformist: it splices the `conformist` input, the
// `conformist` outputs argument, the per-system `let` bindings
// (conformistPkg/eval/impureEval), and the per-system output attributes
// (formatter/checks.formatting/packages.conformist-*/devShells.default),
// preserving the rest of the file byte-for-byte.
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
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
)

//go:embed nix.peg
var nixGrammar []byte

//go:embed outputs.peg
var outputsGrammar []byte

const (
	nixEntry     = "nix.peg"
	outputsEntry = "outputs.peg"
)

// ErrUnrecognized means the flake.nix is not the recognized
// eachDefaultSystem shape (or already binds conformist's names to
// something else). The caller should fall back to print-only.
var ErrUnrecognized = errors.New("flakeedit: flake.nix is not the recognized eachDefaultSystem shape")

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

// the three inputs conform wires in, by top-level input name. text is the
// block-form binding (no leading `inputs.`); flat form prepends it.
var conformistInputs = []struct{ name, text string }{
	{"conformist", `conformist.url = "github:amarbel-llc/conformist";`},
	{"nixpkgs", `nixpkgs.follows = "conformist/nixpkgs-master";`},
	{"utils", `utils.follows = "conformist/utils";`},
}

// the let bindings conform wires in, keyed by name for idempotency.
var conformistLetNames = []string{"conformistPkg", "eval", "impureEval"}

// Apply splices conformist's wiring into src and returns the rewritten
// source plus a report of what changed. It returns ErrUnrecognized (and
// src unchanged) when the flake is not the recognized shape; the caller
// then falls back to print-only.
func Apply(src []byte, opts Options) ([]byte, EditReport, error) {
	matcher, err := newMatcher(nixEntry, nixGrammar)
	if err != nil {
		return src, EditReport{}, fmt.Errorf("flakeedit: compile grammar: %w", err)
	}
	tree, _, err := matcher.Match(src)
	if err != nil {
		return src, EditReport{}, ErrUnrecognized
	}

	ins, ok := findInputsAttrSet(tree, src)
	if !ok {
		return src, EditReport{}, ErrUnrecognized
	}

	valStart, valEnd, ok := outputsValueSpan(tree)
	if !ok {
		return src, EditReport{}, ErrUnrecognized
	}

	outs, ok := parseOutputs(src, valStart, valEnd)
	if !ok {
		return src, EditReport{}, ErrUnrecognized
	}

	// Idempotency sentinel: a fully-wired flake has all three let
	// bindings; a flake that binds some of conformist's names but not all
	// is either a foreign collision or a half-edit we must not deepen, so
	// fall back to print-only.
	have := 0
	for _, n := range conformistLetNames {
		if outs.letExisting[n] {
			have++
		}
	}
	switch have {
	case len(conformistLetNames):
		// already wired; re-run adds only any missing output attrs.
	case 0:
		// clean; full wiring.
	default:
		return src, EditReport{}, ErrUnrecognized
	}
	alreadyWired := have == len(conformistLetNames)

	var (
		report  EditReport
		splices []splice
	)

	// 1. inputs
	if s, labels, ok := inputsSplice(ins); ok {
		splices = append(splices, s)
		report.Added = append(report.Added, labels...)
	}

	// 2. outputs argument
	if !outs.argNames["conformist"] {
		splices = append(splices, splice{
			offset: outs.argInsertOff,
			text:   "\n" + outs.argIndent + "conformist,",
		})
		report.Added = append(report.Added, "outputs arg conformist")
	}

	// 3. let bindings (only when not already wired)
	if !alreadyWired {
		splices = append(splices, letSplice(src, outs))
		report.Added = append(report.Added, "let bindings (conformistPkg, eval, impureEval)")
	}

	// 4. return attributes
	retSplice, added, conflicts := returnSplice(src, outs, alreadyWired, opts.ForceFormatter)
	if retSplice.text != "" {
		splices = append(splices, retSplice)
	}

	report.Added = append(report.Added, added...)
	report.Conflicts = append(report.Conflicts, conflicts...)

	// 5. devShell packages-list merge (#63): when devShells.default already
	// exists and we located its packages list, splice conformist's tools into
	// that list instead of leaving it a conflict (returnSplice skips it).
	if outs.devShellPackages != nil {
		if s, ok := devShellMergeSplice(src, *outs.devShellPackages); ok {
			splices = append(splices, s)
			report.Added = append(report.Added, "devShells.default packages")
		}
	}

	// 6. forced formatter replacement (#63): with --force-formatter, replace an
	// existing formatter's value with conformist's wrapper rather than leaving
	// it a conflict (returnSplice skips it). A no-op when the value is already
	// the wrapper, so re-runs add nothing.
	if opts.ForceFormatter && outs.formatterValue != nil {
		const wrapper = "eval.config.build.wrapper"
		if strings.TrimSpace(string(src[outs.formatterValue.start:outs.formatterValue.end])) != wrapper {
			splices = append(splices, splice{
				offset: outs.formatterValue.start,
				end:    outs.formatterValue.end,
				text:   wrapper,
			})
			report.Added = append(report.Added, "formatter (replaced)")
		}
	}

	if len(splices) == 0 {
		return src, report, nil
	}

	// Apply highest offset first so earlier offsets stay valid. Splices are
	// non-overlapping insertions plus at most one replacement (the forced
	// formatter), so descending order keeps every other splice's offsets valid.
	sort.Slice(splices, func(i, j int) bool { return splices[i].offset > splices[j].offset })
	out := src
	for _, s := range splices {
		out = s.applyTo(out)
	}

	return out, report, nil
}

// splice is one pending edit: text inserted at offset, or — when end > offset —
// replacing the byte range [offset, end).
type splice struct {
	offset int
	end    int
	text   string
}

// applyTo returns src with the splice applied: a replacement of [offset, end)
// when end > offset, otherwise a pure insertion at offset.
func (s splice) applyTo(src []byte) []byte {
	if s.end > s.offset {
		out := make([]byte, 0, len(src)-(s.end-s.offset)+len(s.text))
		out = append(out, src[:s.offset]...)
		out = append(out, s.text...)
		out = append(out, src[s.end:]...)

		return out
	}

	return spliceAt(src, s.offset, s.text)
}

// inputsSplice builds the insertion for the conformist input bindings not
// already present, honoring block vs flat form. ok is false when all
// three inputs already exist.
func inputsSplice(ins inputsAttrSet) (splice, []string, bool) {
	present := ins.topLevelNames()

	var (
		b      strings.Builder
		labels []string
	)
	for _, in := range conformistInputs {
		if present[in.name] {
			continue
		}
		text := in.text
		if !ins.blockMode {
			text = "inputs." + text
		}
		if ins.leadNewline {
			b.WriteString("\n")
			b.WriteString(ins.indent)
			b.WriteString(text)
		} else {
			b.WriteString(ins.indent)
			b.WriteString(text)
			b.WriteString("\n")
		}
		labels = append(labels, "input "+in.name)
	}
	if len(labels) == 0 {
		return splice{}, nil, false
	}
	if ins.leadNewline && ins.trailNewlineIndent != "" {
		b.WriteString("\n")
		b.WriteString(ins.trailNewlineIndent)
	}

	return splice{offset: ins.insertOffset, text: b.String()}, labels, true
}

// letSplice builds the insertion for the conformistPkg/eval/impureEval
// let bindings, placed just before `in`.
func letSplice(src []byte, outs parsedOutputs) splice {
	i := outs.letIndent
	body := "" +
		i + "conformistPkg = conformist.packages.${system}.default;\n" +
		"\n" +
		i + "eval = conformist.lib.evalModule pkgs {\n" +
		i + "  imports = [\n" +
		i + "    conformist.lib.presets.eng\n" +
		i + "    ./conformist.nix\n" +
		i + "  ];\n" +
		i + "  package = conformistPkg;\n" +
		i + "};\n" +
		"\n" +
		i + "impureEval = conformist.lib.evalModule pkgs {\n" +
		i + "  imports = [ conformist.lib.presets.eng-impure ];\n" +
		i + "  package = conformistPkg;\n" +
		i + "  projectRootFile = \"flake.nix\";\n" +
		i + "};\n"

	// Separate the new bindings from the existing let bindings with a
	// blank line.
	return beforeCloser(src, outs.letCloseOff, "\n"+body)
}

// returnAttr is one output attribute conform wires in: path is its
// attr-path (for idempotency), text its rendered binding given an indent.
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
				i + "    pkgs.just\n" +
				i + "  ];\n" +
				i + "};\n"
		}},
	}
}

// returnSplice builds the insertion for the output attributes not already
// present. When the flake is not yet wired, an existing attribute is
// reported as a conflict (left untouched); when already wired, an
// existing attribute is a silent idempotent skip.
func returnSplice(src []byte, outs parsedOutputs, alreadyWired, forceFormatter bool) (splice, []string, []string) {
	i := outs.retIndent

	var (
		body      strings.Builder
		added     []string
		conflicts []string
	)
	for _, a := range returnAttrs() {
		if outs.retExisting[a.path] {
			// devShells.default with a locatable packages list is merged into
			// by the caller's devShell step, so it is neither re-added nor
			// reported as a conflict here.
			if a.path == "devShells.default" && outs.devShellPackages != nil {
				continue
			}

			// formatter with --force-formatter is replaced by the caller's
			// formatter step, so it is likewise neither re-added nor a conflict.
			if a.path == "formatter" && forceFormatter && outs.formatterValue != nil {
				continue
			}

			if !alreadyWired {
				conflicts = append(conflicts, a.path)
			}

			continue
		}

		// If the attr's parent exists as a nested attrset (`packages = { … }`),
		// splicing the dotted form (`packages.conformist-* = …`) would
		// double-define it — invalid Nix. Report a conflict instead of adding
		// (#63); merging into a nested attrset is out of scope.
		if parent, ok := dottedParent(a.path); ok && outs.retExisting[parent] {
			if !alreadyWired {
				conflicts = append(conflicts, a.path)
			}

			continue
		}

		body.WriteString(a.text(i))
		added = append(added, a.path)
	}
	if body.Len() == 0 {
		return splice{}, added, conflicts
	}

	// Separate the new attributes from the existing ones with a blank line.
	return beforeCloser(src, outs.retCloseOff, "\n"+body.String()), added, conflicts
}

// dottedParent returns the parent attr-path of a dotted path
// ("packages.conformist-pre-commit" → "packages"), and false for a
// single-segment path with no parent ("formatter").
func dottedParent(path string) (string, bool) {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i], true
	}

	return "", false
}

// devShellPackages are the tools conform merges into an existing
// devShells.default packages list (#63), in order.
var devShellPackages = []string{
	"conformistPkg",
	"eval.config.build.preCommit",
	"eval.config.build.repair",
	"pkgs.just",
}

// devShellMergeSplice builds the insertion that adds conformist's tools to an
// existing devShells.default packages list, skipping any already present so a
// re-run is a no-op. ok is false when every tool is already in the list.
func devShellMergeSplice(src []byte, ls listSplice) (splice, bool) {
	indent := lineIndent(src, ls.closeOff) + "  "

	var body strings.Builder

	for _, pkg := range devShellPackages {
		if strings.Contains(ls.inner, pkg) {
			continue
		}

		body.WriteString(indent + pkg + "\n")
	}

	if body.Len() == 0 {
		return splice{}, false
	}

	return beforeCloser(src, ls.closeOff, body.String()), true
}

// beforeCloser builds a splice that inserts body just before a closer
// token (a `}` or the `in` keyword) at closeOff. When the closer sits on
// its own line, body is inserted at the start of that line (body must end
// in a newline so the closer stays put). Otherwise body is bracketed with
// newlines so the closer is pushed onto its own line.
func beforeCloser(src []byte, closeOff int, body string) splice {
	if onlyBlankBefore(src, closeOff) {
		return splice{offset: lineStart(src, closeOff), text: body}
	}

	closerIndent := lineIndent(src, closeOff)

	return splice{offset: closeOff, text: "\n" + body + closerIndent}
}
