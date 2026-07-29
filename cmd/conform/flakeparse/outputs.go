package flakeparse

import (
	"regexp"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// ParsedOutputs holds the splice points located inside an `outputs`
// value that matched the eachDefaultSystem shape. All offsets are
// ABSOLUTE in the original flake source.
type ParsedOutputs struct {
	// ArgInsertOff is the byte offset just after the argset's opening
	// brace; a new argument is spliced there. ArgIndent is the
	// indentation of the existing arguments.
	ArgInsertOff int
	ArgIndent    string
	ArgNames     map[string]bool

	// LetCloseOff is the byte offset of the `in` keyword; new let
	// bindings are spliced just before it. LetExisting is the set of
	// binding names already in the let block.
	LetCloseOff int
	LetIndent   string
	LetExisting map[string]bool

	// RetCloseOff is the byte offset of the return attrset's closing
	// brace; new output attributes are spliced just before it.
	// RetExisting is the set of attr-paths already in the return attrset.
	RetCloseOff int
	RetIndent   string
	RetExisting map[string]bool

	// MergeExisting is the set of attr-paths defined on the MERGE side of an
	// eng-hybrid `<attrs> // eachDefaultSystem (…)` (or its trailing spelling).
	// Empty when there is no merge.
	//
	// It exists because `//` gives the RIGHT operand precedence. An attr
	// defined on a trailing merge side SHADOWS the same attr in the per-system
	// body, so wiring spliced into the per-system attrset would be silently
	// overridden — the failure mode would be a conform run that reports success
	// and changes nothing observable. Callers check this and report a conflict
	// rather than writing dead wiring (conformist#65).
	MergeExisting map[string]bool

	// MergeIsTrailing reports whether the merge attrset FOLLOWS the call
	// (`each (…) // { … }`), which is the direction that actually shadows.
	// A leading merge (`{ … } // each (…)`) is overridden BY the per-system
	// body, so a collision there is harmless to the splice.
	MergeIsTrailing bool

	// DevShellPackages, when non-nil, locates the `packages = [ … ]` list
	// inside an existing devShells.default binding, so conform can merge
	// its tools into that list instead of reporting a conflict. Nil when
	// devShells.default is absent or its packages list could not be located.
	DevShellPackages *ListSplice

	// FormatterValue, when non-nil, is the byte range of an existing
	// formatter attribute's value, so conform can replace it under
	// --force-formatter. Nil when formatter is absent.
	FormatterValue *ValueRange
}

// MergeShadows reports whether splicing the attr-path path into the
// per-system return attrset would be overridden by the hybrid's merge side.
//
// Only a TRAILING merge shadows: `each (…) // { … }` gives the merge side
// precedence, so an attr defined there wins over the same attr in the
// per-system body. A LEADING merge (`{ … } // each (…)`) is the other way
// round and is harmless.
//
// Only the ROOT segment of each side is compared, because `//` is a SHALLOW
// update: it replaces whole top-level attrs rather than deep-merging them. A
// merge side spelling `devShells.x86_64-linux.default = …` therefore replaces
// the ENTIRE `devShells` attr, shadowing `devShells.default` in the per-system
// body — so comparing full paths, or only the query's root against a literal
// merge key, would both miss it.
func (p ParsedOutputs) MergeShadows(path string) bool {
	if !p.MergeIsTrailing || len(p.MergeExisting) == 0 {
		return false
	}

	want, _, _ := strings.Cut(path, ".")

	for key := range p.MergeExisting {
		if root, _, _ := strings.Cut(key, "."); root == want {
			return true
		}
	}

	return false
}

// ListSplice locates an existing Nix list for in-place merging: CloseOff
// is the absolute byte offset of its closing `]`, and Inner is the
// list's full text (brackets included) used to skip items already present.
type ListSplice struct {
	CloseOff int
	Inner    string
}

// InnerStart returns the absolute source offset of Inner[0] (the '[').
func (ls ListSplice) InnerStart() int { return ls.CloseOff - len(ls.Inner) + 1 }

// ValueRange is the absolute [Start, End) byte range of a binding's value.
type ValueRange struct {
	Start int
	End   int
}

// outputsValueSpan returns the byte range of the top-level `outputs`
// binding's value in a flake parsed by the nix grammar.
func outputsValueSpan(tree langlang.Tree) (start, end int, ok bool) {
	root, rootOK := tree.Root()
	if !rootOK {
		return 0, 0, false
	}
	seq, seqOK := topAttrSetSequence(tree, root)
	if !seqOK {
		return 0, 0, false
	}
	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != ruleBinding {
			continue
		}
		kv, kvOK := bindingKeyVal(tree, child)
		if !kvOK {
			continue
		}
		path, val, pOK := keyValPath(tree, kv)
		if !pOK || len(path) != 1 || path[0] != "outputs" {
			continue
		}
		span := tree.Span(val)

		return span.Start.Cursor, span.End.Cursor, true
	}

	return 0, 0, false
}

// parseOutputs runs the outputs-shape grammar over the outputs value at
// src[base:end] and returns the located splice points, with offsets
// absolute in src. ok is false when the value does not match the
// eachDefaultSystem shape.
//
// Indents are read from the full src at absolute offsets (not from the
// sliced substring) because the arg set's opening brace is the first
// byte of the substring and would lose its real indentation.
func parseOutputs(src []byte, base, end int) (ParsedOutputs, bool) {
	sub := src[base:end]
	matcher, err := outputsMatcherOnce()
	if err != nil {
		return ParsedOutputs{}, false
	}
	tree, _, err := matcher.Match(sub)
	if err != nil {
		return ParsedOutputs{}, false
	}
	root, ok := tree.Root()
	if !ok {
		return ParsedOutputs{}, false
	}

	var out ParsedOutputs

	outputs, ok := childNamed(tree, root, "Outputs")
	if !ok {
		return ParsedOutputs{}, false
	}
	sysLambda, ok := childNamed(tree, outputs, "SystemLambda")
	if !ok {
		return ParsedOutputs{}, false
	}

	argSet, ok := childNamed(tree, outputs, "ArgSet")
	if !ok {
		return ParsedOutputs{}, false
	}
	braceOpen, ok := childNamed(tree, argSet, "BraceOpen")
	if !ok {
		return ParsedOutputs{}, false
	}
	out.ArgInsertOff = base + tree.Span(braceOpen).End.Cursor
	out.ArgIndent = LineIndent(src, base+tree.Span(braceOpen).Start.Cursor) + "  "
	out.ArgNames = identifiers(tree.Text(argSet))

	inKw, ok := childNamed(tree, sysLambda, "InKw")
	if !ok {
		return ParsedOutputs{}, false
	}
	letClose := tree.Span(inKw).Start.Cursor
	out.LetCloseOff = base + letClose
	out.LetIndent = LineIndent(src, out.LetCloseOff) + "  "

	letBlock, ok := childNamed(tree, sysLambda, "LetBlock")
	if !ok {
		return ParsedOutputs{}, false
	}
	out.LetExisting = bindingNames(tree, letBlock)

	retSet, ok := childNamed(tree, sysLambda, "ReturnAttrSet")
	if !ok {
		return ParsedOutputs{}, false
	}
	retBrace, ok := childNamed(tree, retSet, "BraceClose")
	if !ok {
		return ParsedOutputs{}, false
	}
	retClose := tree.Span(retBrace).Start.Cursor
	out.RetCloseOff = base + retClose
	out.RetIndent = LineIndent(src, out.RetCloseOff) + "  "
	out.RetExisting = bindingPaths(tree, retSet)

	out.MergeExisting = map[string]bool{}

	// A trailing merge shadows the per-system body; a leading one is shadowed
	// by it. Record which, so the caller only treats the dangerous direction
	// as a conflict.
	mergeNode, mergeOK := childNamed(tree, outputs, "TrailMerge")
	if mergeOK {
		out.MergeIsTrailing = true
	} else {
		mergeNode, mergeOK = childNamed(tree, outputs, "LeadMerge")
	}

	if mergeOK {
		if set, ok := childNamed(tree, mergeNode, "MergeSet"); ok {
			if inner, ok := childNamed(tree, set, "Inner"); ok {
				for _, key := range ScanBlockKeys(tree.Text(inner)) {
					out.MergeExisting[key] = true
				}
			}
		}
	}

	if out.RetExisting["devShells.default"] {
		if val, ok := bindingValue(tree, retSet, "devShells.default"); ok {
			if ls, ok := findPackagesList(tree, val, base); ok {
				out.DevShellPackages = &ls
			}
		}
	}

	if out.RetExisting["formatter"] {
		if val, ok := bindingValue(tree, retSet, "formatter"); ok {
			span := tree.Span(val)
			out.FormatterValue = &ValueRange{Start: base + span.Start.Cursor, End: base + span.End.Cursor}
		}
	}

	return out, true
}

// bindingValue returns the Value node of the binding whose dotted attr-path
// equals path, within an attrset node.
func bindingValue(tree langlang.Tree, block langlang.NodeID, path string) (langlang.NodeID, bool) {
	for _, b := range collectBindings(tree, block) {
		kv, ok := bindingKeyVal(tree, b)
		if !ok {
			continue
		}

		segs, val, ok := keyValPath(tree, kv)
		if ok && strings.Join(segs, ".") == path {
			return val, true
		}
	}

	return 0, false
}

// findPackagesList locates a `packages = [ … ]` list inside val and
// returns the absolute offset of its closing `]` plus the list's text.
func findPackagesList(tree langlang.Tree, val langlang.NodeID, base int) (ListSplice, bool) {
	var (
		result ListSplice
		found  bool
	)

	var walk func(n langlang.NodeID)
	walk = func(n langlang.NodeID) {
		var prev string

		for _, c := range valueItems(tree, n) {
			if found {
				return
			}

			if isBracketGroup(tree, c) && packagesAssignment(prev) {
				if bc, ok := childNamed(tree, c, "BracketClose"); ok {
					result = ListSplice{
						CloseOff: base + tree.Span(bc).Start.Cursor,
						Inner:    tree.Text(c),
					}
					found = true

					return
				}
			}

			walk(c)
			prev = tree.Text(c)
		}
	}
	walk(val)

	return result, found
}

// isBracketGroup reports whether group is a `[ … ]` list.
func isBracketGroup(tree langlang.Tree, group langlang.NodeID) bool {
	if nodeName(tree, group) != "Group" {
		return false
	}

	seq, ok := firstSequence(tree, group)
	if !ok {
		return false
	}

	for _, c := range tree.Children(seq) {
		switch nodeName(tree, c) {
		case "BracketOpen":
			return true
		case "BraceOpen", "ParenOpen":
			return false
		}
	}

	return false
}

// packagesAssignmentRE matches text ending in a `packages =` binding LHS.
var packagesAssignmentRE = regexp.MustCompile(`(?s)(?:^|[^\w.])packages\s*=\s*(?:with\s+[\w.]+\s*;\s*)?$`)

func packagesAssignment(prev string) bool {
	return packagesAssignmentRE.MatchString(prev)
}

// bindingNames returns the single-segment names bound at the top level
// of a let block (conformistPkg, eval, …).
func bindingNames(tree langlang.Tree, block langlang.NodeID) map[string]bool {
	names := map[string]bool{}
	for _, b := range collectBindings(tree, block) {
		kv, ok := bindingKeyVal(tree, b)
		if !ok {
			continue
		}
		if path, _, ok := keyValPath(tree, kv); ok && len(path) > 0 {
			names[path[0]] = true
		}
	}

	return names
}

// bindingPaths returns the dotted attr-paths bound at the top level of
// an attrset. When a binding's value is itself a sole attrset, its inner
// keys are also registered under the outer path for idempotency.
func bindingPaths(tree langlang.Tree, block langlang.NodeID) map[string]bool {
	paths := map[string]bool{}
	for _, b := range collectBindings(tree, block) {
		kv, ok := bindingKeyVal(tree, b)
		if !ok {
			continue
		}

		path, val, ok := keyValPath(tree, kv)
		if !ok || len(path) == 0 {
			continue
		}

		base := strings.Join(path, ".")
		paths[base] = true

		if grp, ok := soleGroup(tree, val); ok {
			if inner, ok := childNamed(tree, grp, "Inner"); ok {
				for _, key := range ScanBlockKeys(tree.Text(inner)) {
					paths[base+"."+key] = true
				}
			}
		}
	}

	return paths
}

// collectBindings returns the direct Binding nodes under a node,
// descending through anonymous/Sequence wrappers but never into a
// Binding itself.
func collectBindings(tree langlang.Tree, node langlang.NodeID) []langlang.NodeID {
	var out []langlang.NodeID
	var walk func(n langlang.NodeID)
	walk = func(n langlang.NodeID) {
		for _, c := range tree.Children(n) {
			if nodeName(tree, c) == ruleBinding {
				out = append(out, c)

				continue
			}

			if nodeName(tree, c) == "" || tree.Type(c) == langlang.NodeType_Sequence {
				walk(c)
			}
		}
	}
	walk(node)

	return out
}

// identifiers returns the set of bare identifiers appearing in s (used
// to read the existing argument names out of an arg set's opaque text).
func identifiers(s string) map[string]bool {
	names := map[string]bool{}
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			names[cur.String()] = true
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '_' || r == '\'' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9'):
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()

	return names
}
