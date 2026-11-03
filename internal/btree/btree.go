// Package btree is an in-memory B+ tree keyed by byte strings.
//
// It has the same shape as the on-disk B+ trees in PostgreSQL, InnoDB and
// SQLite: values live only in leaves, leaves are linked left to right for
// range scans, and internal nodes hold separator keys that route a search.
// A disk engine stores each node in one page; here each node is a struct so
// the algorithms stay visible.
package btree

import (
	"bytes"
	"slices"
	"sort"
)

type node struct {
	leaf     bool
	keys     [][]byte
	vals     [][]byte // leaves only, parallel to keys
	children []*node  // internal nodes only, len(children) == len(keys)+1
	next     *node    // leaves only: right sibling
}

// Tree is not safe for concurrent use; callers provide locking.
type Tree struct {
	root    *node
	maxKeys int
	size    int
}

// New returns an empty tree whose nodes split when they exceed maxKeys keys.
// A disk engine derives maxKeys from the page size: an 8 KiB page with 16-byte
// entries holds about 500 keys. Small values such as 3 make splits easy to see.
func New(maxKeys int) *Tree {
	if maxKeys < 3 {
		maxKeys = 3
	}
	return &Tree{root: &node{leaf: true}, maxKeys: maxKeys}
}

// Len returns the number of keys.
func (t *Tree) Len() int { return t.size }

// Height returns the number of levels, counting the leaf level.
func (t *Tree) Height() int {
	h := 1
	for n := t.root; !n.leaf; n = n.children[0] {
		h++
	}
	return h
}

// childIndex picks the child whose key range contains k. children[i] holds
// keys in [keys[i-1], keys[i]).
func childIndex(n *node, k []byte) int {
	return sort.Search(len(n.keys), func(i int) bool { return bytes.Compare(k, n.keys[i]) < 0 })
}

// lowerBound returns the first position in a leaf whose key is >= k.
func lowerBound(n *node, k []byte) int {
	return sort.Search(len(n.keys), func(i int) bool { return bytes.Compare(n.keys[i], k) >= 0 })
}

func (t *Tree) findLeaf(k []byte) *node {
	n := t.root
	for !n.leaf {
		n = n.children[childIndex(n, k)]
	}
	return n
}

// Get returns the value stored under k.
func (t *Tree) Get(k []byte) ([]byte, bool) {
	leaf := t.findLeaf(k)
	i := lowerBound(leaf, k)
	if i < len(leaf.keys) && bytes.Equal(leaf.keys[i], k) {
		return leaf.vals[i], true
	}
	return nil, false
}

// Put inserts k or replaces its value. The tree keeps its own copies.
func (t *Tree) Put(k, v []byte) {
	sep, right, added := t.insert(t.root, slices.Clone(k), slices.Clone(v))
	if added {
		t.size++
	}
	if right != nil {
		// The root split: grow the tree by one level. This is the only way a
		// B+ tree gets taller, which is why every leaf stays at the same depth.
		t.root = &node{keys: [][]byte{sep}, children: []*node{t.root, right}}
	}
}

// insert returns a separator and new right sibling when n splits.
func (t *Tree) insert(n *node, k, v []byte) (sep []byte, right *node, added bool) {
	if n.leaf {
		i := lowerBound(n, k)
		if i < len(n.keys) && bytes.Equal(n.keys[i], k) {
			n.vals[i] = v
			return nil, nil, false
		}
		n.keys = slices.Insert(n.keys, i, k)
		n.vals = slices.Insert(n.vals, i, v)
		if len(n.keys) > t.maxKeys {
			sep, right = splitLeaf(n)
		}
		return sep, right, true
	}
	i := childIndex(n, k)
	s, r, added := t.insert(n.children[i], k, v)
	if r != nil {
		n.keys = slices.Insert(n.keys, i, s)
		n.children = slices.Insert(n.children, i+1, r)
		if len(n.keys) > t.maxKeys {
			sep, right = splitInternal(n)
		}
	}
	return sep, right, added
}

// splitLeaf moves the upper half of n into a new leaf. The first key of the
// new leaf is COPIED up as the separator, because leaves must keep every key.
func splitLeaf(n *node) ([]byte, *node) {
	mid := len(n.keys) / 2
	right := &node{
		leaf: true,
		keys: slices.Clone(n.keys[mid:]),
		vals: slices.Clone(n.vals[mid:]),
		next: n.next,
	}
	clear(n.keys[mid:])
	clear(n.vals[mid:])
	n.keys, n.vals = n.keys[:mid], n.vals[:mid]
	n.next = right
	return right.keys[0], right
}

// splitInternal moves the upper half of n into a new node. The middle key is
// PUSHED up: internal nodes only route, so the key does not stay below.
func splitInternal(n *node) ([]byte, *node) {
	mid := len(n.keys) / 2
	sep := n.keys[mid]
	right := &node{
		keys:     slices.Clone(n.keys[mid+1:]),
		children: slices.Clone(n.children[mid+1:]),
	}
	clear(n.keys[mid:])
	clear(n.children[mid+1:])
	n.keys, n.children = n.keys[:mid], n.children[:mid+1]
	return sep, right
}

// Delete removes k and reports whether it was present.
//
// Deletion is lazy: the key leaves its leaf but nodes are never merged, so a
// leaf may end up empty. Separators stay valid as routing bounds even when the
// key they were copied from is gone. PostgreSQL's nbtree takes a similar line:
// it reclaims only pages that become completely empty, during VACUUM.
func (t *Tree) Delete(k []byte) bool {
	leaf := t.findLeaf(k)
	i := lowerBound(leaf, k)
	if i == len(leaf.keys) || !bytes.Equal(leaf.keys[i], k) {
		return false
	}
	leaf.keys = slices.Delete(leaf.keys, i, i+1)
	leaf.vals = slices.Delete(leaf.vals, i, i+1)
	t.size--
	return true
}

// Scan calls fn for each key in [start, end) in ascending order until fn
// returns false. A nil start means the first key; a nil end means no limit.
func (t *Tree) Scan(start, end []byte, fn func(k, v []byte) bool) {
	n := t.root
	for !n.leaf {
		if start == nil {
			n = n.children[0]
		} else {
			n = n.children[childIndex(n, start)]
		}
	}
	i := 0
	if start != nil {
		i = lowerBound(n, start)
	}
	for ; n != nil; n, i = n.next, 0 {
		for ; i < len(n.keys); i++ {
			if end != nil && bytes.Compare(n.keys[i], end) >= 0 {
				return
			}
			if !fn(n.keys[i], n.vals[i]) {
				return
			}
		}
	}
}
