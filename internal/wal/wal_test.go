package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func replayAll(t *testing.T, path string) (*Log, []string) {
	t.Helper()
	var got []string
	l, err := Open(path, func(_ int64, p []byte) error {
		got = append(got, string(p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return l, got
}

func TestAppendAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	l, _ := replayAll(t, path)
	for _, s := range []string{"a", "bb", "ccc"} {
		if _, err := l.Commit([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	l, got := replayAll(t, path)
	defer l.Close()
	if fmt.Sprint(got) != "[a bb ccc]" {
		t.Fatalf("replayed %v", got)
	}
	// LSNs are byte offsets: 3 frames of 8-byte header + 1, 2, 3 byte payloads.
	if l.Size() != 3*frameHeader+6 {
		t.Fatalf("size = %d", l.Size())
	}
}

func TestTornTailIsTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	l, _ := replayAll(t, path)
	l.Commit([]byte("committed"))
	l.Commit([]byte("in-flight-when-power-failed"))
	l.Close()

	// Simulate a crash in the middle of the second write: drop its last 5 bytes.
	info, _ := os.Stat(path)
	if err := os.Truncate(path, info.Size()-5); err != nil {
		t.Fatal(err)
	}

	l, got := replayAll(t, path)
	if fmt.Sprint(got) != "[committed]" {
		t.Fatalf("after torn write replayed %v", got)
	}
	// New appends go right after the last good record, not after the garbage.
	l.Commit([]byte("after-recovery"))
	l.Close()

	l, got = replayAll(t, path)
	defer l.Close()
	if fmt.Sprint(got) != "[committed after-recovery]" {
		t.Fatalf("replayed %v", got)
	}
}

func TestChecksumStopsReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	l, _ := replayAll(t, path)
	l.Commit([]byte("first"))
	second, _ := l.Commit([]byte("second"))
	l.Commit([]byte("third"))
	l.Close()

	// Flip one payload byte of the second record.
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	f.WriteAt([]byte{'X'}, second+frameHeader)
	f.Close()

	l, got := replayAll(t, path)
	defer l.Close()
	if fmt.Sprint(got) != "[first]" {
		t.Fatalf("replayed %v, want only the record before the bad checksum", got)
	}
}

func TestGroupCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	l, _ := replayAll(t, path)
	const writers, perWriter = 16, 25
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				if _, err := l.Commit(fmt.Appendf(nil, "w%d-%d", w, i)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	commits := int64(writers * perWriter)
	if l.Syncs() > commits {
		t.Fatalf("%d fsyncs for %d commits", l.Syncs(), commits)
	}
	t.Logf("%d commits needed %d fsyncs", commits, l.Syncs())
	l.Close()

	l, got := replayAll(t, path)
	defer l.Close()
	if len(got) != int(commits) {
		t.Fatalf("replayed %d records, want %d", len(got), commits)
	}
}

func TestRejectsHugeRecord(t *testing.T) {
	l, _ := replayAll(t, filepath.Join(t.TempDir(), "wal.log"))
	defer l.Close()
	if _, err := l.Append(make([]byte, MaxRecord+1)); err != ErrTooLarge {
		t.Fatalf("err = %v", err)
	}
}
