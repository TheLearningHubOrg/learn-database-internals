// Package page implements a slotted page: the fixed-size unit that most
// disk-based engines (PostgreSQL heap pages, SQLite b-tree pages, InnoDB
// pages) use to store variable-length records.
//
// Layout of a 4 KiB page:
//
//	+--------+-----------------+--------------------+----------------------+
//	| header | slot array ---> |    free space      | <--- record data     |
//	+--------+-----------------+--------------------+----------------------+
//	0        4                                     freeEnd               4096
//
// The header holds the slot count and the offset where record data starts.
// Each slot is (offset uint16, length uint16). Records grow down from the end
// of the page, slots grow up from the header, and the page is full when they
// meet. A slot whose offset is 0 is free: offset 0 is inside the header, so no
// live record can start there.
package page

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// Size is the page size in bytes. PostgreSQL uses 8 KiB, InnoDB 16 KiB,
	// SQLite 4 KiB by default. 4 KiB matches most file system blocks.
	Size       = 4096
	headerSize = 4
	slotSize   = 4
	// MaxRecord is the largest record that fits in an empty page.
	MaxRecord = Size - headerSize - slotSize
)

var (
	ErrNoSpace  = errors.New("page: not enough free space")
	ErrTooLarge = errors.New("page: record larger than a page")
	ErrBadSlot  = errors.New("page: slot does not hold a record")
	ErrCorrupt  = errors.New("page: corrupt header")
)

var le = binary.LittleEndian

// Page is one slotted page. The zero value is not usable; call New.
type Page struct {
	buf [Size]byte
}

// New returns an empty page.
func New() *Page {
	p := &Page{}
	p.setFreeEnd(Size)
	return p
}

// FromBytes loads a page image read from disk and validates its header.
func FromBytes(b []byte) (*Page, error) {
	if len(b) != Size {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrCorrupt, len(b), Size)
	}
	p := &Page{}
	copy(p.buf[:], b)
	slotsEnd := headerSize + p.NumSlots()*slotSize
	if fe := p.freeEnd(); fe > Size || fe < slotsEnd {
		return nil, fmt.Errorf("%w: freeEnd=%d slotsEnd=%d", ErrCorrupt, fe, slotsEnd)
	}
	return p, nil
}

// Bytes returns the page image to write to disk.
func (p *Page) Bytes() []byte { return p.buf[:] }

// NumSlots returns the number of slots, live or free.
func (p *Page) NumSlots() int { return int(le.Uint16(p.buf[0:2])) }

// FreeSpace returns the contiguous bytes between the slot array and the data.
func (p *Page) FreeSpace() int {
	return p.freeEnd() - (headerSize + p.NumSlots()*slotSize)
}

// Insert stores rec and returns its slot number. The slot number never
// changes for the life of the record, so (pageID, slot) is a stable record ID.
func (p *Page) Insert(rec []byte) (int, error) {
	if len(rec) > MaxRecord {
		return -1, ErrTooLarge
	}
	slot := p.freeSlot()
	need := len(rec)
	if slot < 0 {
		need += slotSize
	}
	if p.FreeSpace() < need {
		p.Compact()
		if p.FreeSpace() < need {
			return -1, ErrNoSpace
		}
	}
	if slot < 0 {
		slot = p.NumSlots()
		p.setNumSlots(slot + 1)
	}
	start := p.freeEnd() - len(rec)
	copy(p.buf[start:], rec)
	p.setFreeEnd(start)
	p.setSlot(slot, start, len(rec))
	return slot, nil
}

// Get returns a copy of the record in slot.
func (p *Page) Get(slot int) ([]byte, error) {
	off, n, err := p.liveSlot(slot)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, p.buf[off:off+n])
	return out, nil
}

// Delete frees slot. The bytes stay in place until Compact reclaims them.
func (p *Page) Delete(slot int) error {
	if _, _, err := p.liveSlot(slot); err != nil {
		return err
	}
	p.setSlot(slot, 0, 0)
	return nil
}

// Compact moves every live record to the end of the page so that the holes
// left by deletes become one contiguous free region. Slot numbers keep their
// meaning; only the offsets stored in the slots change.
func (p *Page) Compact() {
	type live struct {
		slot int
		data []byte
	}
	var recs []live
	for s := 0; s < p.NumSlots(); s++ {
		if rec, err := p.Get(s); err == nil {
			recs = append(recs, live{s, rec})
		}
	}
	end := Size
	for _, r := range recs {
		end -= len(r.data)
		copy(p.buf[end:], r.data)
		p.setSlot(r.slot, end, len(r.data))
	}
	p.setFreeEnd(end)
}

func (p *Page) liveSlot(slot int) (off, n int, err error) {
	if slot < 0 || slot >= p.NumSlots() {
		return 0, 0, ErrBadSlot
	}
	base := headerSize + slot*slotSize
	off = int(le.Uint16(p.buf[base:]))
	n = int(le.Uint16(p.buf[base+2:]))
	if off == 0 || off+n > Size {
		return 0, 0, ErrBadSlot
	}
	return off, n, nil
}

func (p *Page) freeSlot() int {
	for s := 0; s < p.NumSlots(); s++ {
		if le.Uint16(p.buf[headerSize+s*slotSize:]) == 0 {
			return s
		}
	}
	return -1
}

func (p *Page) setSlot(slot, off, n int) {
	base := headerSize + slot*slotSize
	le.PutUint16(p.buf[base:], uint16(off))
	le.PutUint16(p.buf[base+2:], uint16(n))
}

func (p *Page) freeEnd() int       { return int(le.Uint16(p.buf[2:4])) }
func (p *Page) setFreeEnd(off int) { le.PutUint16(p.buf[2:4], uint16(off)) }
func (p *Page) setNumSlots(n int)  { le.PutUint16(p.buf[0:2], uint16(n)) }
