package flakeedit

import (
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// outputsValueSpan returns the byte range of the top-level `outputs`
// binding's value in a flake parsed by the nix grammar. ok is false when
// there is no top-level `outputs = …` binding.
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
		if nodeName(tree, child) != "Binding" {
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

// parsedOutputs holds the splice points located inside an `outputs`
// value that matched the eachDefaultSystem shape. All offsets are
// ABSOLUTE in the original flake source (the substring base is already
// added in).
type parsedOutputs struct {
	// argInsertOff is the byte offset just after the argset's opening
	// brace; a new `conformist,` argument is spliced there (as the first
	// argument, which is valid regardless of trailing commas or a `...`
	// ellipsis). argIndent is the indentation of the existing arguments.
	argInsertOff int
	argIndent    string
	argNames     map[string]bool

	// letCloseOff is the byte offset of the `in` keyword; new let
	// bindings are spliced just before it. letExisting is the set of
	// binding names already in the let block.
	letCloseOff int
	letIndent   string
	letExisting map[string]bool

	// retCloseOff is the byte offset of the return attrset's closing
	// brace; new output attributes are spliced just before it.
	// retExisting is the set of attr-paths already in the return attrset.
	retCloseOff int
	retIndent   string
	retExisting map[string]bool
}

// parseOutputs runs the outputs-shape grammar over the outputs value at
// src[base:end] and returns the located splice points, with offsets
// absolute in src. ok is false when the value does not match the
// eachDefaultSystem shape, so the caller falls back to print-only.
//
// Indents are read from the full src at absolute offsets (not from the
// sliced substring), because the arg set's opening brace is the first
// byte of the substring and so would lose its real indentation.
func parseOutputs(src []byte, base, end int) (parsedOutputs, bool) {
	sub := src[base:end]
	matcher, err := newMatcher(outputsEntry, outputsGrammar)
	if err != nil {
		return parsedOutputs{}, false
	}
	tree, _, err := matcher.Match(sub)
	if err != nil {
		return parsedOutputs{}, false
	}
	root, ok := tree.Root()
	if !ok {
		return parsedOutputs{}, false
	}

	var out parsedOutputs

	// childNamed does not descend through named nodes, so navigate the
	// intermediate Outputs → SystemLambda rules explicitly.
	outputs, ok := childNamed(tree, root, "Outputs")
	if !ok {
		return parsedOutputs{}, false
	}
	sysLambda, ok := childNamed(tree, outputs, "SystemLambda")
	if !ok {
		return parsedOutputs{}, false
	}

	// Arg set: splice point just after '{', indent from the brace line.
	argSet, ok := childNamed(tree, outputs, "ArgSet")
	if !ok {
		return parsedOutputs{}, false
	}
	braceOpen, ok := childNamed(tree, argSet, "BraceOpen")
	if !ok {
		return parsedOutputs{}, false
	}
	out.argInsertOff = base + tree.Span(braceOpen).End.Cursor
	out.argIndent = lineIndent(src, base+tree.Span(braceOpen).Start.Cursor) + "  "
	out.argNames = identifiers(nodeText(tree, argSet))

	// let block: splice point just before `in`.
	inKw, ok := childNamed(tree, sysLambda, "InKw")
	if !ok {
		return parsedOutputs{}, false
	}
	letClose := tree.Span(inKw).Start.Cursor
	out.letCloseOff = base + letClose
	out.letIndent = lineIndent(src, out.letCloseOff) + "  "

	letBlock, ok := childNamed(tree, sysLambda, "LetBlock")
	if !ok {
		return parsedOutputs{}, false
	}
	out.letExisting = bindingNames(tree, letBlock)

	// return attrset: splice point just before its closing brace.
	retSet, ok := childNamed(tree, sysLambda, "ReturnAttrSet")
	if !ok {
		return parsedOutputs{}, false
	}
	// ReturnAttrSet <- BraceOpen Trivia Binding* BraceClose: the closing
	// brace is a direct child (childNamed does not descend into Binding
	// nodes, so nested braces inside binding values are not matched).
	retBrace, ok := childNamed(tree, retSet, "BraceClose")
	if !ok {
		return parsedOutputs{}, false
	}
	retClose := tree.Span(retBrace).Start.Cursor
	out.retCloseOff = base + retClose
	out.retIndent = lineIndent(src, out.retCloseOff) + "  "
	out.retExisting = bindingPaths(tree, retSet)

	return out, true
}

// nodeText returns the source text spanned by a node.
func nodeText(tree langlang.Tree, n langlang.NodeID) string {
	return tree.Text(n)
}

// bindingNames returns the single-segment names bound at the top level of
// a let block (conformistPkg, eval, …) — nested attrset bindings inside
// a binding's value are opaque (not Binding nodes) and are not included.
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

// bindingPaths returns the dotted attr-paths bound at the top level of an
// attrset (formatter, checks.formatting, packages.conformist-repair, …).
func bindingPaths(tree langlang.Tree, block langlang.NodeID) map[string]bool {
	paths := map[string]bool{}
	for _, b := range collectBindings(tree, block) {
		kv, ok := bindingKeyVal(tree, b)
		if !ok {
			continue
		}
		if path, _, ok := keyValPath(tree, kv); ok && len(path) > 0 {
			paths[strings.Join(path, ".")] = true
		}
	}

	return paths
}

// collectBindings returns the direct Binding nodes under a node,
// descending through anonymous/Sequence repetition wrappers but never
// into a Binding itself (so nested attrset bindings inside a binding's
// value are excluded).
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

// identifiers returns the set of bare identifiers appearing in s (used to
// read the existing argument names out of an arg set's opaque text). A
// `...` ellipsis is not an identifier and is ignored.
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
