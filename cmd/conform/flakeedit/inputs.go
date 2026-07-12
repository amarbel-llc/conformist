package flakeedit

import (
	"slices"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// inputsAttrSet describes the located, editable `inputs` region of a
// flake.nix and how to splice new bindings into it. Ported from
// amarbel-llc/doppelgang internal/0/nixedit.
type inputsAttrSet struct {
	// existing is the set of attr-paths already bound under inputs,
	// joined by ".", each in the full `inputs.`-prefixed form (e.g.
	// "inputs.utils.follows"). Used for idempotency.
	existing map[string]bool
	// insertOffset is the byte offset at which new bindings are spliced:
	// just before the `inputs = { … }` block's closing brace (block
	// mode), or just after the last flat inputs.* binding (flat mode).
	insertOffset int
	// indent is the leading whitespace to mirror for spliced lines.
	indent string
	// blockMode is true when splicing inside an `inputs = { … }` block,
	// in which case the leading `inputs.` segment is dropped from each
	// rendered binding (the block already supplies it).
	blockMode bool
	// leadNewline is true when the splice point is mid-line, so each new
	// binding is written as "\n" + indent + text rather than indent +
	// text + "\n".
	leadNewline bool
	// trailNewlineIndent, when non-empty, is written as "\n" + this value
	// after the last binding — used to push a single-line block's closing
	// brace onto its own line.
	trailNewlineIndent string
}

// topLevelNames returns the set of top-level input names already bound
// (the segment immediately after `inputs`), e.g. {"nixpkgs","utils"}.
// Used to skip adding an input whose name already exists in any form
// (`utils.url` precludes adding `utils.follows`).
func (i inputsAttrSet) topLevelNames() map[string]bool {
	names := map[string]bool{}
	for path := range i.existing {
		segs := strings.Split(path, ".")
		if len(segs) >= 2 && segs[0] == "inputs" {
			names[segs[1]] = true
		}
	}

	return names
}

// findInputsAttrSet locates the editable `inputs` region of a parsed
// flake.nix. Supports block form (`inputs = { … }`) and flat form
// (top-level `inputs.<x>.… = …`). Returns ok=false when neither is
// found, so the caller falls back to print-only.
func findInputsAttrSet(tree langlang.Tree, src []byte) (inputsAttrSet, bool) {
	root, ok := tree.Root()
	if !ok {
		return inputsAttrSet{}, false
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return inputsAttrSet{}, false
	}

	existing := map[string]bool{}
	var (
		blockGroup  langlang.NodeID
		haveBlock   bool
		lastFlatKey langlang.NodeID
		haveFlat    bool
		flatIndent  string
	)

	for _, child := range tree.Children(seq) {
		if nodeName(tree, child) != ruleBinding {
			continue
		}

		kv, kvOK := bindingKeyVal(tree, child)
		if !kvOK {
			continue
		}

		path, val, pOK := keyValPath(tree, kv)
		if !pOK || len(path) == 0 || path[0] != "inputs" {
			continue
		}

		if len(path) == 1 {
			// `inputs = <value>`: block form if value is an attrset.
			if g, gOK := soleGroup(tree, val); gOK {
				blockGroup = g
				haveBlock = true
			}

			continue
		}
		// `inputs.<x>… = …`: flat form. Record the full attr-path.
		existing[strings.Join(path, ".")] = true
		lastFlatKey = kv
		haveFlat = true
		flatIndent = lineIndent(src, tree.Span(child).Start.Cursor)
	}

	switch {
	case haveBlock:
		return blockInsert(tree, src, blockGroup)
	case haveFlat:
		off := afterSemicolon(src, tree.Span(lastFlatKey).End.Cursor)

		return inputsAttrSet{
			existing:     existing,
			insertOffset: off,
			indent:       flatIndent,
			blockMode:    false,
			leadNewline:  true,
		}, true
	default:
		return inputsAttrSet{}, false
	}
}

// blockInsert computes the splice point inside a block-form
// `inputs = { … }` attrset: just before its closing brace. Existing
// inner binding keys are recovered from the group's opaque text and
// normalized to the full `inputs.`-prefixed form for idempotency.
func blockInsert(tree langlang.Tree, src []byte, group langlang.NodeID) (inputsAttrSet, bool) {
	var (
		brace    langlang.NodeID
		haveCl   bool
		innerTxt string
	)
	for _, n := range tree.Children(group) {
		if tree.Type(n) == langlang.NodeType_Sequence {
			for _, c := range tree.Children(n) {
				switch nodeName(tree, c) {
				case "BraceClose":
					brace = c
					haveCl = true
				case "Inner":
					innerTxt = tree.Text(c)
				}
			}
		}
	}
	if !haveCl {
		return inputsAttrSet{}, false
	}
	closeOff := tree.Span(brace).Start.Cursor
	braceIndent := lineIndent(src, closeOff)

	existing := map[string]bool{}
	for _, key := range scanBlockKeys(innerTxt) {
		existing["inputs."+key] = true
	}

	ins := inputsAttrSet{
		existing:  existing,
		indent:    braceIndent + "  ",
		blockMode: true,
	}

	if onlyBlankBefore(src, closeOff) {
		ins.insertOffset = lineStart(src, closeOff)
	} else {
		ins.insertOffset = closeOff
		ins.leadNewline = true
		ins.trailNewlineIndent = braceIndent
	}

	return ins, true
}

// scanBlockKeys extracts the LHS attr-paths of `a.b.c = …;` bindings from
// the opaque text of an `inputs` attrset block. A deliberately simple
// line scanner (not a parser): for each `;`-terminated segment it takes
// the text before the first `=`, drops `#` line comments, and uses the
// last non-blank line. Used only for idempotency.
func scanBlockKeys(inner string) []string {
	var keys []string
	for seg := range strings.SplitSeq(inner, ";") {
		before, _, found := strings.Cut(seg, "=")
		if !found {
			continue
		}

		lhs := attrPathBeforeEquals(before)
		if lhs == "" || !isAttrPath(lhs) {
			continue
		}

		keys = append(keys, lhs)
	}

	return keys
}

// attrPathBeforeEquals returns the bare attr-path that immediately
// precedes a binding's `=`, given the text before that `=`. Drops `#`
// line comments and earlier lines, returning the trimmed last non-blank
// line. Returns "" if none remains.
func attrPathBeforeEquals(before string) string {
	lines := strings.Split(before, "\n")
	for _, line := range slices.Backward(lines) {
		if c := strings.Index(line, "#"); c >= 0 {
			line = line[:c]
		}

		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}

	return ""
}

// isAttrPath reports whether s is a dotted run of bare identifiers, e.g.
// "utils.inputs.systems.follows". Quoted segments and interpolation are
// not recognized (conservative: such a key just won't dedupe).
func isAttrPath(s string) bool {
	for seg := range strings.SplitSeq(s, ".") {
		if seg == "" {
			return false
		}
		for i, r := range seg {
			ok := r == '_' || r == '-' || r == '\'' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(i > 0 && r >= '0' && r <= '9')
			if !ok {
				return false
			}
		}
	}

	return true
}
