package page

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestInsertGet(t *testing.T) {
	p := New()
	a, _ := p.Insert([]byte("alpha"))
	b, _ := p.Insert([]byte("bravo"))
	if a != 0 || b != 1 {
		t.Fatalf("slots = %d, %d; want 0, 1", a, b)
	}
	got, err := p.Get(b)
	if err != nil || string(got) != "bravo" {
		t.Fatalf("Get(1) = %q, %v", got, err)
	}
	if want := Size - headerSize - 2*slotSize - 10; p.FreeSpace() != want {
		t.Fatalf("FreeSpace = %d, want %d", p.FreeSpace(), want)
	}
}

func TestDeleteReusesSlot(t *testing.T) {
	p := New()
	p.Insert([]byte("one"))
	p.Insert([]byte("two"))
	if err := p.Delete(0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Get(0); !errors.Is(err, ErrBadSlot) {
		t.Fatalf("Get deleted slot: err = %v, want ErrBadSlot", err)
	}
	s, _ := p.Insert([]byte("three"))
	if s != 0 {
		t.Fatalf("reused slot = %d, want 0", s)
	}
	if p.NumSlots() != 2 {
		t.Fatalf("NumSlots = %d, want 2", p.NumSlots())
	}
}

func TestFillThenCompact(t *testing.T) {
	p := New()
	rec := bytes.Repeat([]byte("x"), 100)
	n := 0
	for {
		if _, err := p.Insert(rec); err != nil {
			if !errors.Is(err, ErrNoSpace) {
				t.Fatal(err)
			}
			break
		}
		n++
	}
	// 4092 usable bytes / 104 bytes per record (100 data + 4 slot) = 39.
	if n != 39 {
		t.Fatalf("fitted %d records, want 39", n)
	}
	// Delete every other record: the space is fragmented, not contiguous.
	for s := 0; s < n; s += 2 {
		p.Delete(s)
	}
	big := bytes.Repeat([]byte("y"), 1000)
	slot, err := p.Insert(big)
	if err != nil {
		t.Fatalf("insert after deletes should compact and succeed: %v", err)
	}
	got, _ := p.Get(slot)
	if !bytes.Equal(got, big) {
		t.Fatal("record corrupted by compaction")
	}
	// Records that survived keep their slot numbers.
	for s := 1; s < n; s += 2 {
		if got, err := p.Get(s); err != nil || !bytes.Equal(got, rec) {
			t.Fatalf("slot %d lost after compaction: %v", s, err)
		}
	}
}

func TestRoundTripBytes(t *testing.T) {
	p := New()
	for i := range 20 {
		p.Insert(fmt.Appendf(nil, "row-%02d", i))
	}
	q, err := FromBytes(p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(7)
	if string(got) != "row-07" {
		t.Fatalf("got %q", got)
	}
}

func TestFromBytesRejectsGarbage(t *testing.T) {
	junk := bytes.Repeat([]byte{0xFF}, Size)
	if _, err := FromBytes(junk); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
	if _, err := FromBytes(make([]byte, 10)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short page: err = %v, want ErrCorrupt", err)
	}
}

func TestTooLarge(t *testing.T) {
	p := New()
	if _, err := p.Insert(make([]byte, MaxRecord+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Insert(make([]byte, MaxRecord)); err != nil {
		t.Fatalf("max-size record should fit: %v", err)
	}
}
