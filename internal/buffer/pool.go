// Package buffer is a buffer pool: a fixed set of in-memory frames that cache
// disk pages, with pin counts, dirty bits and CLOCK replacement.
package buffer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/page"
)

// PageID numbers pages from 0. Page n lives at byte offset n*page.Size.
type PageID uint32

// Disk reads and writes whole pages.
type Disk interface {
	ReadPage(id PageID, buf []byte) error
	WritePage(id PageID, buf []byte) error
}

// FileDisk stores pages in one file.
type FileDisk struct{ F *os.File }

// ReadPage reads page id. Pages past the end of the file read as zeros, which
// lets the pool allocate a new page by fetching the next unused id.
func (d FileDisk) ReadPage(id PageID, buf []byte) error {
	n, err := d.F.ReadAt(buf, int64(id)*page.Size)
	if errors.Is(err, io.EOF) {
		clear(buf[n:])
		return nil
	}
	return err
}

// WritePage writes page id in place. It does not fsync: durability comes from
// the WAL, and the pool only writes a dirty page after its log records are
// durable (see Stage 2).
func (d FileDisk) WritePage(id PageID, buf []byte) error {
	_, err := d.F.WriteAt(buf, int64(id)*page.Size)
	return err
}

// ErrNoFreeFrame means every frame is pinned.
var ErrNoFreeFrame = errors.New("buffer: all frames are pinned")

type frame struct {
	id    PageID
	data  []byte
	pins  int
	dirty bool
	ref   bool // CLOCK reference bit: set on access, cleared by the sweeping hand
	valid bool
}

// Pool is safe for concurrent use. The mutex protects pool metadata only;
// callers that change page bytes need their own per-page latch.
type Pool struct {
	mu     sync.Mutex
	disk   Disk
	frames []frame
	table  map[PageID]int // page id -> frame index
	hand   int

	// Counters. Read them only while no other goroutine is using the pool.
	Hits, Misses, Evictions, Writes int
}

// NewPool returns a pool with n frames.
func NewPool(disk Disk, n int) *Pool {
	p := &Pool{disk: disk, frames: make([]frame, n), table: make(map[PageID]int, n)}
	for i := range p.frames {
		p.frames[i].data = make([]byte, page.Size)
	}
	return p
}

// Fetch pins page id and returns its bytes. The slice stays valid until the
// matching Unpin; after that the frame may be reused for another page.
func (p *Pool) Fetch(id PageID) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i, ok := p.table[id]; ok {
		f := &p.frames[i]
		f.pins++
		f.ref = true
		p.Hits++
		return f.data, nil
	}
	p.Misses++
	i := p.victim()
	if i < 0 {
		return nil, ErrNoFreeFrame
	}
	f := &p.frames[i]
	if f.valid {
		if f.dirty {
			if err := p.disk.WritePage(f.id, f.data); err != nil {
				return nil, fmt.Errorf("buffer: write back page %d: %w", f.id, err)
			}
			p.Writes++
		}
		delete(p.table, f.id)
		p.Evictions++
	}
	if err := p.disk.ReadPage(id, f.data); err != nil {
		f.valid = false
		return nil, err
	}
	*f = frame{id: id, data: f.data, pins: 1, ref: true, valid: true}
	p.table[id] = i
	return f.data, nil
}

// victim picks a frame to reuse: an empty one if any, otherwise the CLOCK
// algorithm. The hand sweeps the frames; a frame with its reference bit set
// gets a second chance (the bit is cleared), a frame without it is evicted.
// Two full sweeps without a candidate means every frame is pinned.
func (p *Pool) victim() int {
	for i := range p.frames {
		if !p.frames[i].valid {
			return i
		}
	}
	for range 2 * len(p.frames) {
		i := p.hand
		p.hand = (p.hand + 1) % len(p.frames)
		f := &p.frames[i]
		if f.pins > 0 {
			continue
		}
		if f.ref {
			f.ref = false
			continue
		}
		return i
	}
	return -1
}

// Unpin releases one pin on page id. Pass dirty=true if the caller modified
// the page.
func (p *Pool) Unpin(id PageID, dirty bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, ok := p.table[id]
	if !ok || p.frames[i].pins == 0 {
		return fmt.Errorf("buffer: unpin of page %d that is not pinned", id)
	}
	p.frames[i].pins--
	p.frames[i].dirty = p.frames[i].dirty || dirty
	return nil
}

// FlushAll writes every dirty page. A checkpoint does this (Stage 2).
func (p *Pool) FlushAll() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.frames {
		f := &p.frames[i]
		if f.valid && f.dirty {
			if err := p.disk.WritePage(f.id, f.data); err != nil {
				return err
			}
			f.dirty = false
			p.Writes++
		}
	}
	return nil
}
