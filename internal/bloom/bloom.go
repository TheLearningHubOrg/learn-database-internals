// Package bloom is a Bloom filter: a bit array that answers "definitely not
// present" or "maybe present". LSM engines keep one per SSTable so that a
// point lookup can skip files that cannot contain the key.
package bloom

import (
	"hash/fnv"
	"math"
)

// Filter is not safe for concurrent writes.
type Filter struct {
	bits []uint64
	m    uint64 // number of bits
	k    int    // number of hash probes
}

// New sizes a filter for n keys at false-positive rate p using the standard
// formulas m = -n*ln(p)/ln(2)^2 and k = (m/n)*ln(2). At p = 0.01 that is about
// 9.6 bits and 7 probes per key.
func New(n int, p float64) *Filter {
	if n < 1 {
		n = 1
	}
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
	if m < 64 {
		m = 64
	}
	k := int(math.Round(float64(m) / float64(n) * math.Ln2))
	if k < 1 {
		k = 1
	}
	return &Filter{bits: make([]uint64, (m+63)/64), m: m, k: k}
}

// hashes derives two hashes from one FNV-1a pass. Probe i uses h1 + i*h2
// (Kirsch and Mitzenmacher), which is as good as k independent hashes.
func hashes(key []byte) (uint64, uint64) {
	h := fnv.New64a()
	h.Write(key)
	h1 := h.Sum64()
	// splitmix64 finaliser: decorrelates h2 from h1.
	h2 := h1 + 0x9E3779B97F4A7C15
	h2 = (h2 ^ (h2 >> 30)) * 0xBF58476D1CE4E5B9
	h2 = (h2 ^ (h2 >> 27)) * 0x94D049BB133111EB
	h2 ^= h2 >> 31
	return h1, h2 | 1
}

// Add records key.
func (f *Filter) Add(key []byte) {
	h1, h2 := hashes(key)
	for i := range f.k {
		bit := (h1 + uint64(i)*h2) % f.m
		f.bits[bit/64] |= 1 << (bit % 64)
	}
}

// MayContain returns false only if key was never added.
func (f *Filter) MayContain(key []byte) bool {
	h1, h2 := hashes(key)
	for i := range f.k {
		bit := (h1 + uint64(i)*h2) % f.m
		if f.bits[bit/64]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

// Probes returns k, the number of bits set per key.
func (f *Filter) Probes() int { return f.k }

// Bits returns m, the size of the filter in bits.
func (f *Filter) Bits() uint64 { return f.m }
