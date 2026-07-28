package flakeparse

import (
	"strings"

	langlang "github.com/clarete/langlang/go"
)

// ruleBinding is the grammar rule name for an attrset/let binding node.
const ruleBinding = "Binding"

// nodeName returns the grammar rule name of a node, or "" for nodes that
// carry no name. langlang's Tree.Name panics on String/Sequence/non-Node
// nodes (their nameID is -1), so all name lookups go through this guard.
func nodeName(tree langlang.Tree, n langlang.NodeID) string {
	if tree.Type(n) == langlang.NodeType_Node {
		return tree.Name(n)
	}

	return ""
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

// topAttrSetSequence returns the Sequence node holding the top-level
// attribute set's children (BraceOpen, Trivia, Binding*, BraceClose).
func topAttrSetSequence(tree langlang.Tree, root langlang.NodeID) (langlang.NodeID, bool) {
	attrset, ok := childNamed(tree, root, "AttrSet")
	if !ok {
		return 0, false
	}

	return firstSequence(tree, attrset)
}

// bindingKeyVal returns the KeyVal node inside a Binding. Inherit
// bindings yield false.
func bindingKeyVal(tree langlang.Tree, binding langlang.NodeID) (langlang.NodeID, bool) {
	seq, ok := firstSequence(tree, binding)
	if !ok {
		return 0, false
	}

	return childNamed(tree, seq, "KeyVal")
}

// keyValPath returns the attr-path segments and the Value node of a KeyVal.
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
// (i.e. `inputs = { … }`), false otherwise. The match is strict.
func soleGroup(tree langlang.Tree, val langlang.NodeID) (langlang.NodeID, bool) {
	var (
		group   langlang.NodeID
		haveOne bool
	)
	for _, c := range valueItems(tree, val) {
		switch nodeName(tree, c) {
		case "Group":
			if haveOne {
				return 0, false
			}
			group = c
			haveOne = true
		case "OuterText":
			if strings.TrimSpace(tree.Text(c)) != "" {
				return 0, false
			}
		default:
			return 0, false
		}
	}

	return group, haveOne
}

// valueItems returns the direct alternation members of a Value node,
// descending through a single anonymous Sequence wrapper if present.
func valueItems(tree langlang.Tree, val langlang.NodeID) []langlang.NodeID {
	children := tree.Children(val)
	if len(children) == 1 && tree.Type(children[0]) == langlang.NodeType_Sequence {
		return tree.Children(children[0])
	}

	return children
}
