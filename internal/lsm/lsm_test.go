package lsm

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func k(i int) []byte { return fmt.Appendf(nil, "key-%05d", i) }

func TestNewestVersionWins(t *testing.T) {
	tr := New(Options{MemtableBytes: 1 << 30, MaxRuns: 10})
	tr.Put([]byte("a"), []byte("v1"))
	tr.Flush()
	tr.Put([]byte("a"), []byte("v2"))
	tr.Flush()
	tr.Put([]byte("a"), []byte("v3")) // still in the memtable
	if v, _ := tr.Get([]byte("a")); string(v) != "v3" {
		t.Fatalf("memtable read = %q", v)
	}
	tr.Flush()
	if tr.Runs() != 3 {
		t.Fatalf("runs = %d", tr.Runs())
	}
	if v, _ := tr.Get([]byte("a")); string(v) != "v3" {
		t.Fatalf("after flush = %q", v)
	}
}

func TestTombstoneShadowsOlderRun(t *testing.T) {
	tr := New(Options{MemtableBytes: 1 << 30, MaxRuns: 10})
	tr.Put([]byte("gone"), []byte("old"))
	tr.Flush()
	tr.Delete([]byte("gone"))
	tr.Flush()
	if _, ok := tr.Get([]byte("gone")); ok {
		t.Fatal("tombstone in the newer run must hide the older value")
	}
	tr.Compact()
	if tr.Runs() != 1 || len(tr.runs[0].entries) != 0 {
		t.Fatalf("compaction should drop the key and its tombstone: %d runs, %d entries",
			tr.Runs(), len(tr.runs[0].entries))
	}
}

func TestBloomFiltersSkipRuns(t *testing.T) {
	tr := New(Options{MemtableBytes: 1 << 30, MaxRuns: 100})
	for r := range 8 {
		for i := range 500 {
			tr.Put(k(r*1000+i), []byte("x"))
		}
		tr.Flush()
	}
	before := tr.Stats()
	for i := range 1000 {
		tr.Get(k(900_000 + i)) // keys that exist in no run
	}
	s := tr.Stats()
	skips, probes := s.FilterSkips-before.FilterSkips, s.RunProbes-before.RunProbes
	t.Logf("8000 run checks: %d skipped by Bloom filters, %d binary searches", skips, probes)
	if probes > 8000/20 {
		t.Fatalf("Bloom filters let %d of 8000 run checks through", probes)
	}
}

func TestAgainstMap(t *testing.T) {
	tr := New(Options{MemtableBytes: 2 << 10, MaxRuns: 3})
	model := map[string]string{}
	rng := rand.New(rand.NewPCG(7, 7))
	for op := range 20_000 {
		key := k(rng.IntN(2000))
		if rng.IntN(5) == 0 {
			tr.Delete(key)
			delete(model, string(key))
		} else {
			v := fmt.Sprint(op)
			tr.Put(key, []byte(v))
			model[string(key)] = v
		}
	}
	if tr.Stats().Flushes == 0 || tr.Stats().Compactions == 0 {
		t.Fatalf("test did not exercise flush and compaction: %+v", tr.Stats())
	}
	for i := range 2000 {
		got, ok := tr.Get(k(i))
		want, wantOK := model[string(k(i))]
		if ok != wantOK || string(got) != want {
			t.Fatalf("Get(%s) = %q,%v; want %q,%v", k(i), got, ok, want, wantOK)
		}
	}
}
