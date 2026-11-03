package btree

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

func key(i int) []byte { return fmt.Appendf(nil, "k%06d", i) }

// check verifies the B+ tree invariants and returns the leaf depth.
func check(t *testing.T, tr *Tree, n *node, lo, hi []byte, depth int) int {
	t.Helper()
	if len(n.keys) > tr.maxKeys {
		t.Fatalf("node has %d keys, max %d", len(n.keys), tr.maxKeys)
	}
	for i, k := range n.keys {
		if i > 0 && bytes.Compare(n.keys[i-1], k) >= 0 {
			t.Fatalf("keys out of order: %q >= %q", n.keys[i-1], k)
		}
		if lo != nil && bytes.Compare(k, lo) < 0 || hi != nil && bytes.Compare(k, hi) >= 0 {
			t.Fatalf("key %q outside [%q, %q)", k, lo, hi)
		}
	}
	if n.leaf {
		if len(n.vals) != len(n.keys) {
			t.Fatal("leaf keys and vals differ in length")
		}
		return depth
	}
	if len(n.children) != len(n.keys)+1 {
		t.Fatalf("internal node: %d keys, %d children", len(n.keys), len(n.children))
	}
	leafDepth := -1
	for i, c := range n.children {
		clo, chi := lo, hi
		if i > 0 {
			clo = n.keys[i-1]
		}
		if i < len(n.keys) {
			chi = n.keys[i]
		}
		d := check(t, tr, c, clo, chi, depth+1)
		if leafDepth >= 0 && d != leafDepth {
			t.Fatalf("leaves at depths %d and %d", leafDepth, d)
		}
		leafDepth = d
	}
	return leafDepth
}

func TestAgainstMap(t *testing.T) {
	for _, maxKeys := range []int{3, 4, 7, 64} {
		t.Run(fmt.Sprint("maxKeys=", maxKeys), func(t *testing.T) {
			tr := New(maxKeys)
			model := map[string]string{}
			rng := rand.New(rand.NewPCG(1, uint64(maxKeys)))
			for op := range 5000 {
				k := key(rng.IntN(800))
				switch rng.IntN(4) {
				case 0:
					delete(model, string(k))
					tr.Delete(k)
				default:
					v := fmt.Sprint(op)
					model[string(k)] = v
					tr.Put(k, []byte(v))
				}
			}
			check(t, tr, tr.root, nil, nil, 1)
			if tr.Len() != len(model) {
				t.Fatalf("Len = %d, model has %d", tr.Len(), len(model))
			}
			for k, want := range model {
				if got, ok := tr.Get([]byte(k)); !ok || string(got) != want {
					t.Fatalf("Get(%s) = %q, %v; want %q", k, got, ok, want)
				}
			}
			var scanned []string
			tr.Scan(nil, nil, func(k, _ []byte) bool {
				scanned = append(scanned, string(k))
				return true
			})
			want := make([]string, 0, len(model))
			for k := range model {
				want = append(want, k)
			}
			slices.Sort(want)
			if !slices.Equal(scanned, want) {
				t.Fatalf("full scan returned %d keys in wrong order", len(scanned))
			}
		})
	}
}

func TestRangeScan(t *testing.T) {
	tr := New(4)
	for i := range 100 {
		tr.Put(key(i), nil)
	}
	var got []string
	tr.Scan(key(10), key(15), func(k, _ []byte) bool {
		got = append(got, string(k))
		return true
	})
	if fmt.Sprint(got) != "[k000010 k000011 k000012 k000013 k000014]" {
		t.Fatalf("scan [10,15) = %v", got)
	}
	n := 0
	tr.Scan(key(90), nil, func(_, _ []byte) bool { n++; return n < 3 })
	if n != 3 {
		t.Fatalf("early stop: fn called %d times", n)
	}
}

func TestHeightIsLogarithmic(t *testing.T) {
	tr := New(64)
	for i := range 100_000 {
		tr.Put(key(i), nil)
	}
	// Sequential inserts leave leaves half full (32 keys), so the tree needs
	// ceil(log_33(100000 / 32)) + 1 = 4 levels at most.
	if h := tr.Height(); h > 4 {
		t.Fatalf("height %d for 100k keys with fanout 64", h)
	}
}

func TestPutCopiesInput(t *testing.T) {
	tr := New(4)
	k, v := []byte("key"), []byte("value")
	tr.Put(k, v)
	k[0], v[0] = 'X', 'X'
	if got, ok := tr.Get([]byte("key")); !ok || string(got) != "value" {
		t.Fatalf("caller's buffers leaked into the tree: %q %v", got, ok)
	}
}
