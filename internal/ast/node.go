package ast

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// FindNodeAtPosition returns the deepest node (named or anonymous) at the given
// zero-indexed line and column, or nil if root is nil. It may return anonymous
// leaf nodes such as literal tokens and punctuation.
//
// When the position falls in a gap between tokens — for example the editor
// cursor sitting just after the space that follows a keyword — it returns the
// token immediately preceding it on the same line (the node the user just
// typed) rather than an enclosing ancestor. A position exactly at the end of a
// token is treated as a clean boundary and resolves to the enclosing node.
func FindNodeAtPosition(root *sitter.Node, line, col uint) *sitter.Node {
	if root == nil {
		return nil
	}
	p := sitter.Point{Row: line, Column: col}
	node := root.DescendantForPointRange(p, p)
	// Guard nil BEFORE dereferencing node (B1, chunt-mpc): an out-of-range
	// position can make DescendantForPointRange return nil, and the GrammarName
	// call below would then panic. (The prior nil check was dead code, sitting
	// after the deref.)
	if node == nil {
		return nil
	}
	if node.GrammarName() == "text" {
		return node.Parent()
	}
	if node.ChildCount() == 0 {
		if pointEqual(node.EndPosition(), p) {
			return node.Parent()
		}
		return node
	}
	prev := deepestLeafEndingAtOrBefore(root, p)
	if prev != nil && !pointEqual(prev.EndPosition(), p) && prev.EndPosition().Row == p.Row {
		return prev
	}
	return node
}

// deepestLeafEndingAtOrBefore descends from root into the right-most child whose
// range ends at or before p, returning the deepest such leaf.
func deepestLeafEndingAtOrBefore(root *sitter.Node, p sitter.Point) *sitter.Node {
	cur := root
	for cur.ChildCount() > 0 {
		var next *sitter.Node
		for i := uint(0); i < cur.ChildCount(); i++ {
			c := cur.Child(i)
			if c != nil && pointLE(c.EndPosition(), p) {
				next = c
			}
		}
		if next == nil {
			break
		}
		cur = next
	}
	return cur
}

func pointLE(a, b sitter.Point) bool {
	return a.Row < b.Row || (a.Row == b.Row && a.Column <= b.Column)
}

func pointEqual(a, b sitter.Point) bool {
	return a.Row == b.Row && a.Column == b.Column
}

func ChildByFieldName(node *sitter.Node, name string) *sitter.Node {
	if node == nil {
		return nil
	}
	return node.ChildByFieldName(name)
}

// WalkNamed depth-first traverses the named-children subtree of n, invoking
// visit on each node. If visit returns false, the subtree under that node is
// skipped. Shared by the diagnostic passes (chunt-mpc) to avoid each pass
// re-declaring an identical private walker.
func WalkNamed(n *sitter.Node, visit func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		WalkNamed(n.NamedChild(i), visit)
	}
}

// NamedChildByKind returns the first direct named child of node whose kind
// matches, or nil if none is found. Useful for reaching named sub-sections
// (e.g. a "service_section") whose fields do not propagate to the parent.
func NamedChildByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}
