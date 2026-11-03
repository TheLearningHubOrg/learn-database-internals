// Package lsm is a small, in-memory log-structured merge tree. It keeps the
// moving parts of LevelDB, RocksDB and Cassandra (memtable, immutable sorted
// runs, Bloom filters, tombstones, merge compaction) and leaves out files,
// so that each step can be read and tested on its own.
package lsm

import (
	"bytes"
	"container/heap"
	"sort"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/bloom"
	"github.com/TheLearningHubOrg/learn-database-internals/internal/btree"
)

const (
	live      byte = 0
	tombstone byte = 1
)

type entry struct {
	key, value []byte
	deleted    bool
}

// run is an immutable sorted array of entries: the in-memory stand-in for an
// SSTable file.
type run struct {
	entries []entry
	filter  *bloom.Filter
}

func newRun(entries []entry) *run {
	f := bloom.New(len(entries), 0.01)
	for _, e := range entries {
		f.Add(e.key)
	}
	return &run{entries: entries, filter: f}
}

func (r *run) find(k []byte) (entry, bool) {
	i := sort.Search(len(r.entries), func(i int) bool { return bytes.Compare(r.entries[i].key, k) >= 0 })
	if i < len(r.entries) && bytes.Equal(r.entries[i].key, k) {
		return r.entries[i], true
	}
	return entry{}, false
}

// Options control when the tree flushes and compacts.
type Options struct {
	MemtableBytes int // flush the memtable once it holds this many key+value bytes
	MaxRuns       int // compact all runs into one when there are more than this
}

// Stats count the work the tree has done.
type Stats struct {
	Flushes     int
	Compactions int
	RunProbes   int // binary searches into a run
	FilterSkips int // runs skipped because the Bloom filter said "absent"
}

// Tree is not safe for concurrent use.
type Tree struct {
	opts     Options
	mem      *btree.Tree
	memBytes int
	runs     []*run // newest first
	stats    Stats
}

// New returns an empty tree.
func New(opts Options) *Tree {
	if opts.MemtableBytes <= 0 {
		opts.MemtableBytes = 4 << 20
	}
	if opts.MaxRuns <= 0 {
		opts.MaxRuns = 4
	}
	return &Tree{opts: opts, mem: btree.New(64)}
}

// Put writes k=v into the memtable. Writes never touch existing runs: that is
// what makes LSM writes sequential and cheap.
func (t *Tree) Put(k, v []byte) { t.write(k, append([]byte{live}, v...)) }

// Delete writes a tombstone. The old value stays in older runs until a
// compaction meets the tombstone and drops both.
func (t *Tree) Delete(k []byte) { t.write(k, []byte{tombstone}) }

func (t *Tree) write(k, tagged []byte) {
	t.mem.Put(k, tagged)
	t.memBytes += len(k) + len(tagged)
	if t.memBytes >= t.opts.MemtableBytes {
		t.Flush()
	}
}

// Get searches the memtable, then each run from newest to oldest. The first
// hit wins, and a tombstone hit means "deleted".
func (t *Tree) Get(k []byte) ([]byte, bool) {
	if tagged, ok := t.mem.Get(k); ok {
		if tagged[0] == tombstone {
			return nil, false
		}
		return tagged[1:], true
	}
	for _, r := range t.runs {
		if !r.filter.MayContain(k) {
			t.stats.FilterSkips++
			continue
		}
		t.stats.RunProbes++
		if e, ok := r.find(k); ok {
			if e.deleted {
				return nil, false
			}
			return e.value, true
		}
	}
	return nil, false
}

// Flush turns the memtable into a new, newest run.
func (t *Tree) Flush() {
	if t.mem.Len() == 0 {
		return
	}
	entries := make([]entry, 0, t.mem.Len())
	t.mem.Scan(nil, nil, func(k, tagged []byte) bool {
		entries = append(entries, entry{key: k, value: tagged[1:], deleted: tagged[0] == tombstone})
		return true
	})
	t.runs = append([]*run{newRun(entries)}, t.runs...)
	t.mem = btree.New(64)
	t.memBytes = 0
	t.stats.Flushes++
	if len(t.runs) > t.opts.MaxRuns {
		t.Compact()
	}
}

// Compact merges every run into one. Because the result is the oldest data
// in the tree, tombstones have nothing left to shadow and are dropped.
func (t *Tree) Compact() {
	if len(t.runs) < 2 {
		return
	}
	t.runs = []*run{newRun(merge(t.runs, true))}
	t.stats.Compactions++
}

// Runs returns the number of sorted runs.
func (t *Tree) Runs() int { return len(t.runs) }

// Stats returns counters since the tree was created.
func (t *Tree) Stats() Stats { return t.stats }

// merge performs a k-way merge of runs (newest first). For duplicate keys the
// entry from the newest run wins.
func merge(runs []*run, dropTombstones bool) []entry {
	h := &mergeHeap{runs: runs}
	for i, r := range runs {
		if len(r.entries) > 0 {
			h.cur = append(h.cur, cursor{run: i})
		}
	}
	heap.Init(h)
	var out []entry
	var last []byte
	first := true
	for h.Len() > 0 {
		c := h.cur[0]
		e := runs[c.run].entries[c.pos]
		if first || !bytes.Equal(e.key, last) {
			first, last = false, e.key
			if !(dropTombstones && e.deleted) {
				out = append(out, e)
			}
		}
		if c.pos+1 < len(runs[c.run].entries) {
			h.cur[0].pos++
			heap.Fix(h, 0)
		} else {
			heap.Pop(h)
		}
	}
	return out
}

type cursor struct{ run, pos int }

type mergeHeap struct {
	runs []*run
	cur  []cursor
}

func (h *mergeHeap) Len() int { return len(h.cur) }
func (h *mergeHeap) Less(i, j int) bool {
	a, b := h.cur[i], h.cur[j]
	if c := bytes.Compare(h.runs[a.run].entries[a.pos].key, h.runs[b.run].entries[b.pos].key); c != 0 {
		return c < 0
	}
	return a.run < b.run // same key: newer run first
}
func (h *mergeHeap) Swap(i, j int) { h.cur[i], h.cur[j] = h.cur[j], h.cur[i] }
func (h *mergeHeap) Push(x any)    { h.cur = append(h.cur, x.(cursor)) }
func (h *mergeHeap) Pop() any {
	x := h.cur[len(h.cur)-1]
	h.cur = h.cur[:len(h.cur)-1]
	return x
}
