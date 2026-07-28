package flakeparse

import (
	"slices"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// InputsAttrSet describes the located, editable `inputs` region of a
// flake.nix and how to splice new bindings into it.
type InputsAttrSet struct {
	// Existing is the set of attr-paths already bound under inputs, joined
	// by ".", each in full `inputs.`-prefixed form (e.g.
	// "inputs.utils.follows"). Used for idempotency.
	Existing map[string]bool
	// InsertOffset is the byte offset at which new bindings are spliced:
	// just before the `inputs = { … }` block's closing brace (block mode),
	// or just after the last flat inputs.* binding (flat mode).
	InsertOffset int
	// Indent is the leading whitespace to mirror for spliced lines.
	Indent string
	// BlockMode is true when splicing inside an `inputs = { … }` block,
	// in which case the leading `inputs.` segment is dropped from each
	// rendered binding.
	BlockMode bool
	// LeadNewline is true when the splice point is mid-line, so each new
	// binding is written as "\n" + Indent + text rather than Indent + text
	// + "\n".
	LeadNewline bool
	// TrailNewlineIndent, when non-empty, is written as "\n" + this value
	// after the last binding — used to push a single-line block's closing
	// brace onto its own line.
	TrailNewlineIndent string
}

// TopLevelNames returns the set of top-level input names already bound
// (the segment immediately after `inputs`), e.g. {"nixpkgs","utils"}.
func (i InputsAttrSet) TopLevelNames() map[string]bool {
	names := map[string]bool{}
	for path := range i.Existing {
		segs := strings.Split(path, ".")
		if len(segs) >= 2 && segs[0] == "inputs" {
			names[segs[1]] = true
		}
	}

	return names
}

// FindInputsAttrSet locates the editable `inputs` region of a parsed
// flake.nix. Supports block form (`inputs = { … }`) and flat form
// (top-level `inputs.<x>.… = …`). Returns ok=false when neither is found.
func FindInputsAttrSet(tree langlang.Tree, src []byte) (InputsAttrSet, bool) {
	root, ok := tree.Root()
	if !ok {
		return InputsAttrSet{}, false
	}
	seq, ok := topAttrSetSequence(tree, root)
	if !ok {
		return InputsAttrSet{}, false
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
			if g, gOK := soleGroup(tree, val); gOK {
				blockGroup = g
				haveBlock = true
			}

			continue
		}
		existing[strings.Join(path, ".")] = true
		lastFlatKey = kv
		haveFlat = true
		flatIndent = LineIndent(src, tree.Span(child).Start.Cursor)
	}

	switch {
	case haveBlock:
		return blockInsert(tree, src, blockGroup)
	case haveFlat:
		off := afterSemicolon(src, tree.Span(lastFlatKey).End.Cursor)

		return InputsAttrSet{
			Existing:     existing,
			InsertOffset: off,
			Indent:       flatIndent,
			BlockMode:    false,
			LeadNewline:  true,
		}, true
	default:
		return InputsAttrSet{}, false
	}
}

// blockInsert computes the splice point inside a block-form
// `inputs = { … }` attrset: just before its closing brace.
func blockInsert(tree langlang.Tree, src []byte, group langlang.NodeID) (InputsAttrSet, bool) {
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
		return InputsAttrSet{}, false
	}
	closeOff := tree.Span(brace).Start.Cursor
	braceIndent := LineIndent(src, closeOff)

	existing := map[string]bool{}
	for _, key := range ScanBlockKeys(innerTxt) {
		existing["inputs."+key] = true
	}

	ins := InputsAttrSet{
		Existing:  existing,
		Indent:    braceIndent + "  ",
		BlockMode: true,
	}

	if OnlyBlankBefore(src, closeOff) {
		ins.InsertOffset = LineStart(src, closeOff)
	} else {
		ins.InsertOffset = closeOff
		ins.LeadNewline = true
		ins.TrailNewlineIndent = braceIndent
	}

	return ins, true
}

// ScanBlockKeys extracts the LHS attr-paths of `a.b.c = …;` bindings
// from the opaque text of an `inputs` attrset block.
func ScanBlockKeys(inner string) []string {
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

// attrPathBeforeEquals returns the bare attr-path immediately preceding
// a binding's `=`. Drops `#` line comments and earlier lines.
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

// isAttrPath reports whether s is a dotted run of bare identifiers.
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
