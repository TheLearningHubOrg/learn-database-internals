// Package kv is the starter project's storage engine: a write-ahead log for
// durability plus an in-memory B+ tree index for lookups and range scans.
//
// Every write follows the WAL rule: the log record is appended before the
// index changes, and Put returns only after the record is durable. On Open the
// log is replayed into a fresh index, which is crash recovery in its simplest
// form (redo-only, no checkpoints; Stage 2 explains what real engines add).
package kv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/btree"
	"github.com/TheLearningHubOrg/learn-database-internals/internal/record"
	"github.com/TheLearningHubOrg/learn-database-internals/internal/wal"
)

const (
	opPut    byte = 1
	opDelete byte = 2
)

// ErrEmptyKey is returned for zero-length keys.
var ErrEmptyKey = errors.New("kv: empty key")

// Options configure a Store.
type Options struct {
	// NoSync skips fsync on each write. Writes are faster and an OS crash or
	// power loss can lose the most recent ones; a process crash cannot.
	NoSync bool
}

// Store is safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	log   *wal.Log
	index *btree.Tree
	opts  Options
}

// Open opens the store in dir, creating it if needed, and replays the log.
func Open(dir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{index: btree.New(64), opts: opts}
	log, err := wal.Open(filepath.Join(dir, "wal.log"), s.redo)
	if err != nil {
		return nil, err
	}
	s.log = log
	return s, nil
}

// redo applies one logged operation to the index.
func (s *Store) redo(_ int64, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("empty log record")
	}
	key, value, _, err := record.Decode(payload[1:])
	if err != nil {
		return err
	}
	switch payload[0] {
	case opPut:
		s.index.Put(key, value)
	case opDelete:
		s.index.Delete(key)
	default:
		return fmt.Errorf("unknown op %d", payload[0])
	}
	return nil
}

func (s *Store) write(op byte, key, value []byte) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	payload := record.Append([]byte{op}, key, value)

	// Append and apply under the lock so log order equals index order.
	s.mu.Lock()
	lsn, err := s.log.Append(payload)
	if err == nil {
		err = s.redo(lsn, payload)
	}
	s.mu.Unlock()
	if err != nil || s.opts.NoSync {
		return err
	}
	// Wait for durability outside the lock so that concurrent writers share
	// one fsync (group commit). A reader may see this value a few
	// microseconds before it is durable; the caller is only told "done"
	// after it is.
	return s.log.SyncTo(lsn)
}

// Put stores value under key.
func (s *Store) Put(key, value []byte) error { return s.write(opPut, key, value) }

// Delete removes key. Deleting a missing key is not an error.
func (s *Store) Delete(key []byte) error { return s.write(opDelete, key, nil) }

// Get returns a copy of the value stored under key.
func (s *Store) Get(key []byte) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.index.Get(key)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// Pair is one key/value from a scan.
type Pair struct {
	Key, Value []byte
}

// Scan returns up to limit pairs with start <= key < end, in key order.
// A nil end means no upper bound.
func (s *Store) Scan(start, end []byte, limit int) []Pair {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Pair
	s.index.Scan(start, end, func(k, v []byte) bool {
		out = append(out, Pair{append([]byte(nil), k...), append([]byte(nil), v...)})
		return len(out) < limit
	})
	return out
}

// Len returns the number of live keys.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index.Len()
}

// LogSize returns the WAL size in bytes.
func (s *Store) LogSize() int64 { return s.log.Size() }

// Close flushes and closes the log.
func (s *Store) Close() error { return s.log.Close() }
