// Package wal is an append-only write-ahead log with checksummed frames,
// torn-tail recovery and group commit.
//
// Frame layout (little endian):
//
//	+-------------+-------------+-----------------+
//	| crc32c (4)  | length (4)  | payload (length)|
//	+-------------+-------------+-----------------+
//
// The checksum covers the length field and the payload. A record's LSN (log
// sequence number) is the byte offset where its frame starts.
package wal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/fsutil"
)

const (
	frameHeader = 8
	// MaxRecord bounds a single payload so that a corrupt length field cannot
	// make recovery allocate gigabytes.
	MaxRecord = 16 << 20
)

var (
	castagnoli = crc32.MakeTable(crc32.Castagnoli)
	le         = binary.LittleEndian

	ErrTooLarge = errors.New("wal: record exceeds MaxRecord")
	ErrClosed   = errors.New("wal: log is closed")
)

// Log is safe for concurrent use.
type Log struct {
	mu   sync.Mutex // guards f writes, size, err
	f    *os.File
	size int64 // next LSN; everything before it has been written (not yet synced)
	err  error // sticky: after a failed write or fsync the log refuses work

	syncMu sync.Mutex // serialises fsync; guards synced
	synced int64      // every byte before this offset is durable

	syncs atomic.Int64 // number of fsync calls, for tests and metrics
}

// Open opens or creates the log at path. Every intact record is passed to
// apply in LSN order. The first frame that is short or fails its checksum
// marks the end of the log: it and everything after it is truncated, because
// it can only be a write that was in flight when the process or machine died.
func Open(path string, apply func(lsn int64, payload []byte) error) (*Log, error) {
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	end, err := scan(f, apply)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Truncate(end); err != nil {
		f.Close()
		return nil, fmt.Errorf("wal: truncate torn tail: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	if created {
		// A new file's directory entry is only durable once the directory
		// itself is fsynced.
		if err := fsutil.SyncDir(filepath.Dir(path)); err != nil {
			f.Close()
			return nil, err
		}
	}
	return &Log{f: f, size: end, synced: end}, nil
}

func scan(f *os.File, apply func(int64, []byte) error) (int64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	r := bufio.NewReaderSize(f, 64<<10)
	var off int64
	var hdr [frameHeader]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return off, nil
			}
			return 0, err
		}
		sum, n := le.Uint32(hdr[0:4]), le.Uint32(hdr[4:8])
		if n > MaxRecord {
			return off, nil // garbage length: torn or never-written frame
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return off, nil
			}
			return 0, err
		}
		if checksum(hdr[4:8], payload) != sum {
			return off, nil
		}
		if apply != nil {
			if err := apply(off, payload); err != nil {
				return 0, fmt.Errorf("wal: apply record at LSN %d: %w", off, err)
			}
		}
		off += frameHeader + int64(n)
	}
}

func checksum(length, payload []byte) uint32 {
	return crc32.Update(crc32.Checksum(length, castagnoli), castagnoli, payload)
}

// Append writes payload to the OS page cache and returns its LSN. The record
// is NOT durable until SyncTo(lsn) or Sync returns.
func (l *Log) Append(payload []byte) (int64, error) {
	if len(payload) > MaxRecord {
		return 0, ErrTooLarge
	}
	frame := make([]byte, frameHeader+len(payload))
	le.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[frameHeader:], payload)
	le.PutUint32(frame[0:4], checksum(frame[4:8], payload))

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return 0, l.err
	}
	lsn := l.size
	if _, err := l.f.WriteAt(frame, lsn); err != nil {
		l.err = fmt.Errorf("wal: write failed, log is read-only: %w", err)
		return 0, l.err
	}
	l.size += int64(len(frame))
	return lsn, nil
}

// SyncTo returns once the record at lsn is durable. Concurrent callers share
// fsyncs: while one goroutine is inside fsync the others queue on syncMu, and
// the next fsync covers every record appended in the meantime. This is group
// commit.
func (l *Log) SyncTo(lsn int64) error {
	l.syncMu.Lock()
	defer l.syncMu.Unlock()
	if l.synced > lsn {
		return nil // an earlier fsync already covered this record
	}
	l.mu.Lock()
	if l.err != nil {
		err := l.err
		l.mu.Unlock()
		return err
	}
	target := l.size
	l.mu.Unlock()

	l.syncs.Add(1)
	if err := l.f.Sync(); err != nil {
		// Never retry a failed fsync and assume success: the kernel may have
		// already dropped the dirty pages. Fail the log; recovery replays it.
		l.mu.Lock()
		l.err = fmt.Errorf("wal: fsync failed, log is read-only: %w", err)
		l.mu.Unlock()
		return err
	}
	l.synced = target
	return nil
}

// Commit appends payload and waits until it is durable.
func (l *Log) Commit(payload []byte) (int64, error) {
	lsn, err := l.Append(payload)
	if err != nil {
		return 0, err
	}
	return lsn, l.SyncTo(lsn)
}

// Sync makes every appended record durable.
func (l *Log) Sync() error {
	l.mu.Lock()
	end := l.size
	l.mu.Unlock()
	if end == 0 {
		return nil
	}
	return l.SyncTo(end - 1)
}

// Size returns the next LSN, which is also the log length in bytes.
func (l *Log) Size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// Syncs reports how many fsync calls the log has issued.
func (l *Log) Syncs() int64 { return l.syncs.Load() }

// Close syncs and closes the file.
func (l *Log) Close() error {
	if err := l.Sync(); err != nil {
		l.f.Close()
		return err
	}
	l.mu.Lock()
	l.err = ErrClosed
	l.mu.Unlock()
	return l.f.Close()
}
