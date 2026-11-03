package buffer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/page"
)

// memDisk is a Disk that keeps pages in a map and counts I/O.
type memDisk struct {
	pages         map[PageID][]byte
	reads, writes int
}

func newMemDisk() *memDisk { return &memDisk{pages: map[PageID][]byte{}} }

func (d *memDisk) ReadPage(id PageID, buf []byte) error {
	d.reads++
	if p, ok := d.pages[id]; ok {
		copy(buf, p)
	} else {
		clear(buf)
	}
	return nil
}

func (d *memDisk) WritePage(id PageID, buf []byte) error {
	d.writes++
	d.pages[id] = append([]byte(nil), buf...)
	return nil
}

func TestHitAndMiss(t *testing.T) {
	p := NewPool(newMemDisk(), 2)
	p.Fetch(1)
	p.Unpin(1, false)
	p.Fetch(1)
	p.Unpin(1, false)
	if p.Hits != 1 || p.Misses != 1 {
		t.Fatalf("hits=%d misses=%d", p.Hits, p.Misses)
	}
}

func TestDirtyPageWrittenOnEviction(t *testing.T) {
	d := newMemDisk()
	p := NewPool(d, 1)
	buf, _ := p.Fetch(7)
	copy(buf, "hello")
	p.Unpin(7, true)
	if d.writes != 0 {
		t.Fatal("a dirty page must not be written before it is evicted or flushed")
	}
	p.Fetch(8) // only one frame: page 7 must be evicted and written back
	p.Unpin(8, false)
	if d.writes != 1 || string(d.pages[7][:5]) != "hello" {
		t.Fatalf("writes=%d page7=%q", d.writes, d.pages[7][:5])
	}
	buf, _ = p.Fetch(7)
	if string(buf[:5]) != "hello" {
		t.Fatalf("re-read page 7 = %q", buf[:5])
	}
}

func TestPinnedPagesAreNeverEvicted(t *testing.T) {
	p := NewPool(newMemDisk(), 2)
	p.Fetch(1)
	p.Fetch(2)
	if _, err := p.Fetch(3); !errors.Is(err, ErrNoFreeFrame) {
		t.Fatalf("err = %v, want ErrNoFreeFrame", err)
	}
	p.Unpin(2, false)
	if _, err := p.Fetch(3); err != nil {
		t.Fatalf("after unpin: %v", err)
	}
	if _, ok := p.table[1]; !ok {
		t.Fatal("pinned page 1 was evicted")
	}
}

func TestClockSecondChance(t *testing.T) {
	p := NewPool(newMemDisk(), 3)
	for _, id := range []PageID{1, 2, 3} {
		p.Fetch(id)
		p.Unpin(id, false)
	}
	// Page 4 forces an eviction. Every frame has ref=1, so the hand clears all
	// three bits and evicts frame 0 (page 1) on its second pass.
	p.Fetch(4)
	p.Unpin(4, false)
	// Touch page 2 so it earns a second chance; page 3 has ref=0.
	p.Fetch(2)
	p.Unpin(2, false)
	p.Fetch(5) // hand is at frame 1 (page 2, ref=1) -> skip, frame 2 (page 3) -> evict
	p.Unpin(5, false)
	if _, ok := p.table[2]; !ok {
		t.Fatal("recently used page 2 was evicted")
	}
	if _, ok := p.table[3]; ok {
		t.Fatal("page 3 should have been the CLOCK victim")
	}
}

func TestUnpinErrors(t *testing.T) {
	p := NewPool(newMemDisk(), 1)
	if err := p.Unpin(9, false); err == nil {
		t.Fatal("unpin of an unknown page should fail")
	}
}

func TestFileDiskWithSlottedPage(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "heap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pool := NewPool(FileDisk{F: f}, 4)

	buf, _ := pool.Fetch(0) // past EOF: reads as zeros, a fresh page
	pg := page.New()
	slot, _ := pg.Insert([]byte("row stored through the buffer pool"))
	copy(buf, pg.Bytes())
	pool.Unpin(0, true)
	if err := pool.FlushAll(); err != nil {
		t.Fatal(err)
	}

	raw := make([]byte, page.Size)
	if _, err := f.ReadAt(raw, 0); err != nil {
		t.Fatal(err)
	}
	onDisk, err := page.FromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := onDisk.Get(slot)
	if string(got) != "row stored through the buffer pool" {
		t.Fatalf("on disk: %q", got)
	}
}
