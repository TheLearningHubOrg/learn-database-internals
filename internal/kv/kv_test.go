package kv

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPutGetDelete(t *testing.T) {
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Put([]byte("lang"), []byte("go"))
	if v, ok := s.Get([]byte("lang")); !ok || string(v) != "go" {
		t.Fatalf("Get = %q %v", v, ok)
	}
	s.Delete([]byte("lang"))
	if _, ok := s.Get([]byte("lang")); ok {
		t.Fatal("deleted key still readable")
	}
	if err := s.Put(nil, []byte("x")); err != ErrEmptyKey {
		t.Fatalf("empty key: %v", err)
	}
}

func TestRecoveryReplaysLog(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, Options{})
	for i := range 100 {
		s.Put(fmt.Appendf(nil, "k%03d", i), fmt.Appendf(nil, "v%d", i))
	}
	s.Put([]byte("k007"), []byte("overwritten"))
	s.Delete([]byte("k050"))
	s.Close()

	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Len() != 99 {
		t.Fatalf("Len after recovery = %d, want 99", s.Len())
	}
	if v, _ := s.Get([]byte("k007")); string(v) != "overwritten" {
		t.Fatalf("k007 = %q", v)
	}
	if _, ok := s.Get([]byte("k050")); ok {
		t.Fatal("delete was lost in recovery")
	}
}

func TestRecoveryAfterTornWrite(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, Options{})
	s.Put([]byte("safe"), []byte("1"))
	s.Put([]byte("torn"), []byte("2"))
	s.Close()

	path := filepath.Join(dir, "wal.log")
	info, _ := os.Stat(path)
	os.Truncate(path, info.Size()-1)

	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.Get([]byte("safe")); !ok {
		t.Fatal("complete record lost")
	}
	if _, ok := s.Get([]byte("torn")); ok {
		t.Fatal("torn record must not be applied")
	}
}

func TestScan(t *testing.T) {
	s, _ := Open(t.TempDir(), Options{NoSync: true})
	defer s.Close()
	for _, k := range []string{"user:3", "user:1", "order:9", "user:2"} {
		s.Put([]byte(k), []byte("x"))
	}
	got := s.Scan([]byte("user:"), []byte("user;"), 10) // ';' sorts right after ':'
	if len(got) != 3 || string(got[0].Key) != "user:1" || string(got[2].Key) != "user:3" {
		t.Fatalf("prefix scan = %v", got)
	}
	if got := s.Scan(nil, nil, 2); len(got) != 2 {
		t.Fatalf("limit ignored: %d pairs", len(got))
	}
}

func TestConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, Options{})
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				if err := s.Put(fmt.Appendf(nil, "w%d-%02d", w, i), []byte("v")); err != nil {
					t.Error(err)
				}
				s.Get([]byte("w0-00"))
			}
		}()
	}
	wg.Wait()
	s.Close()
	s, _ = Open(dir, Options{})
	defer s.Close()
	if s.Len() != 400 {
		t.Fatalf("Len = %d, want 400", s.Len())
	}
}
