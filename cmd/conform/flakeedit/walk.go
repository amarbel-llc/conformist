package flakeedit

import (
	"fmt"
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// ruleBinding is the grammar rule name for an attrset/let binding node.
const ruleBinding = "Binding"

// newMatcher compiles a CST-mode PEG grammar into a langlang Matcher via
// the in-memory loader. The grammars (nix.peg, outputs.peg) manage
// whitespace explicitly via Trivia rules, so langlang's automatic
// Spacing injection is disabled (the equivalent of the -disable-spaces
// CLI flag). The lower-level loader + database + QueryMatcher path is
// used instead of MatcherFromBytes, which does not exist in langlang
// v0.0.12.
//
// Ported from amarbel-llc/doppelgang internal/0/nixedit/walk.go.
//
//nolint:ireturn // langlang.Matcher is the library's own interface type
func newMatcher(entry string, grammar []byte) (langlang.Matcher, error) {
	cfg := langlang.NewConfig()
	cfg.SetBool("grammar.handle_spaces", false)
	loader := langlang.NewInMemoryImportLoader()
	loader.Add(entry, grammar)
	db := langlang.NewDatabase(cfg, loader)

	m, err := langlang.QueryMatcher(db, entry)
	if err != nil {
		return nil, fmt.Errorf("compile grammar %s: %w", entry, err)
	}

	return m, nil
}

// nodeName returns the grammar rule name of a node, or "" for nodes that
// carry no name. langlang's Tree.Name panics (index [-1]) on String,
// Sequence, and other non-Node nodes because their nameID is -1, so all
// name lookups go through this guard.
func nodeName(tree langlang.Tree, n langlang.NodeID) string {
	if tree.Type(n) == langlang.NodeType_Node {
		return tree.Name(n)
	}

	return ""
}

// topAttrSetSequence returns the Sequence node holding the top-level
// attribute set's children (BraceOpen, Trivia, Binding*, BraceClose).
// Walks File → AttrSet → Sequence.
func topAttrSetSequence(tree langlang.Tree, root langlang.NodeID) (langlang.NodeID, bool) {
	attrset, ok := childNamed(tree, root, "AttrSet")
	if !ok {
		return 0, false
	}

	return firstSequence(tree, attrset)
}

// bindingKeyVal returns the KeyVal node inside a Binding (Binding →
// Sequence → [KeyVal|Inherit, Trivia]). Inherit bindings yield false.
func bindingKeyVal(tree langlang.Tree, binding langlang.NodeID) (langlang.NodeID, bool) {
	seq, ok := firstSequence(tree, binding)
	if !ok {
		return 0, false
	}

	return childNamed(tree, seq, "KeyVal")
}

// keyValPath returns the attr-path segments and the Value node of a
// KeyVal (KeyVal → Sequence → [AttrPath, Trivia, Equals, Trivia, Value,
// Semi]).
func keyValPath(tree langlang.Tree, kv langlang.NodeID) ([]string, langlang.NodeID, bool) {
	seq, ok := firstSequence(tree, kv)
	if !ok {
		return nil, 0, false
	}
	ap, ok := childNamed(tree, seq, "AttrPath")
	if !ok {
		return nil, 0, false
	}
	val, ok := childNamed(tree, seq, "Value")
	if !ok {
		return nil, 0, false
	}

	return attrPathSegments(tree, ap), val, true
}

// attrPathSegments returns the identifier/string segments of an AttrPath
// node in order.
func attrPathSegments(tree langlang.Tree, ap langlang.NodeID) []string {
	var segs []string
	tree.Visit(ap, func(n langlang.NodeID) bool {
		switch nodeName(tree, n) {
		case "Identifier":
			segs = append(segs, tree.Text(n))
		case "String":
			segs = append(segs, unquote(tree.Text(n)))
		}

		return true
	})

	return segs
}

// soleGroup returns the Group node when a Value is *exactly* one Group
// (i.e. `inputs = { … }`), false otherwise. The match is strict: the
// Value's only non-trivia content must be the Group. An `inputs` value
// that is a larger expression containing a group — `let … in { … }`,
// `f { … }`, `{ … } // { … }` — is rejected so block mode is not
// mis-applied to it (the caller then bails to print-only rather than
// splicing into the wrong braces). OuterText that is only whitespace is
// tolerated so surrounding blank lines/newlines around the `{ … }` do
// not disqualify it.
func soleGroup(tree langlang.Tree, val langlang.NodeID) (langlang.NodeID, bool) {
	var (
		group   langlang.NodeID
		haveOne bool
	)
	for _, c := range valueItems(tree, val) {
		switch nodeName(tree, c) {
		case "Group":
			if haveOne {
				return 0, false // more than one group → compound expr
			}
			group = c
			haveOne = true
		case "OuterText":
			if strings.TrimSpace(tree.Text(c)) != "" {
				return 0, false // real tokens around the group
			}
		default:
			return 0, false
		}
	}

	return group, haveOne
}

// valueItems returns the direct alternation members of a Value node,
// descending through a single anonymous Sequence wrapper if present
// (langlang renders a multi-item `(A / B)*` as a Sequence, a single item
// as the item directly under the named Value node).
func valueItems(tree langlang.Tree, val langlang.NodeID) []langlang.NodeID {
	children := tree.Children(val)
	if len(children) == 1 && tree.Type(children[0]) == langlang.NodeType_Sequence {
		return tree.Children(children[0])
	}

	return children
}

// childNamed returns the first direct (or single-child) descendant of n
// whose rule name matches. It descends through anonymous wrapper nodes
// (Sequence/Node with one child) to find a named child one level down.
func childNamed(tree langlang.Tree, n langlang.NodeID, name string) (langlang.NodeID, bool) {
	for _, c := range tree.Children(n) {
		if nodeName(tree, c) == name {
			return c, true
		}
		if nodeName(tree, c) == "" || tree.Type(c) == langlang.NodeType_Sequence {
			if inner, ok := childNamed(tree, c, name); ok {
				return inner, true
			}
		}
	}

	return 0, false
}

// firstSequence returns the first Sequence node at or just below n.
func firstSequence(tree langlang.Tree, n langlang.NodeID) (langlang.NodeID, bool) {
	if tree.Type(n) == langlang.NodeType_Sequence {
		return n, true
	}
	for _, c := range tree.Children(n) {
		if tree.Type(c) == langlang.NodeType_Sequence {
			return c, true
		}
	}

	return 0, false
}

// spliceAt inserts ins at byte offset off in src.
func spliceAt(src []byte, off int, ins string) []byte {
	out := make([]byte, 0, len(src)+len(ins))
	out = append(out, src[:off]...)
	out = append(out, ins...)
	out = append(out, src[off:]...)

	return out
}

// lineStart returns the byte offset of the start of the line containing
// off (the index just after the preceding newline, or 0).
func lineStart(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	i := off
	for i > 0 && src[i-1] != '\n' {
		i--
	}

	return i
}

// lineIndent returns the leading whitespace of the line containing byte
// offset off in src.
func lineIndent(src []byte, off int) string {
	if off > len(src) {
		off = len(src)
	}
	start := off
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	i := start
	for i < off && (src[i] == ' ' || src[i] == '\t') {
		i++
	}

	return string(src[start:i])
}

// onlyBlankBefore reports whether everything between the start of off's
// line and off is whitespace — i.e. off (a closing brace) sits alone on
// its own line, so a line-start splice is safe.
func onlyBlankBefore(src []byte, off int) bool {
	for i := lineStart(src, off); i < off; i++ {
		if src[i] != ' ' && src[i] != '\t' {
			return false
		}
	}

	return true
}

// unquote strips surrounding double quotes from a quoted attr-name
// segment. Best-effort; leaves the text as-is if not double-quoted.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}

	return s
}

// afterSemicolon advances off past an immediately-following ';' (skipping
// intervening whitespace, including newlines), so a flat-binding insert
// lands after the last binding's terminator even when the ';' is written
// on a following line. If no ';' is found it returns off unchanged.
func afterSemicolon(src []byte, off int) int {
	i := off
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
		i++
	}
	if i < len(src) && src[i] == ';' {
		return i + 1
	}

	return off
}
