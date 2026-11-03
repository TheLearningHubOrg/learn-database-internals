package bloom

import (
	"fmt"
	"testing"
)

func TestNoFalseNegatives(t *testing.T) {
	f := New(10_000, 0.01)
	for i := range 10_000 {
		f.Add(fmt.Appendf(nil, "present-%d", i))
	}
	for i := range 10_000 {
		if !f.MayContain(fmt.Appendf(nil, "present-%d", i)) {
			t.Fatalf("false negative for key %d", i)
		}
	}
}

func TestFalsePositiveRate(t *testing.T) {
	const n = 10_000
	f := New(n, 0.01)
	for i := range n {
		f.Add(fmt.Appendf(nil, "present-%d", i))
	}
	fp := 0
	for i := range n {
		if f.MayContain(fmt.Appendf(nil, "absent-%d", i)) {
			fp++
		}
	}
	rate := float64(fp) / n
	t.Logf("m=%d bits (%.1f bits/key), k=%d, false-positive rate %.4f", f.Bits(), float64(f.Bits())/n, f.Probes(), rate)
	if rate > 0.02 {
		t.Fatalf("false-positive rate %.4f, want about 0.01", rate)
	}
	if f.Probes() != 7 {
		t.Fatalf("k = %d, want 7 for p = 0.01", f.Probes())
	}
}
