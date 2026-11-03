package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CURRENT")
	for _, v := range []string{"MANIFEST-000001\n", "MANIFEST-000002\n"} {
		if err := WriteFileAtomic(path, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != v {
			t.Fatalf("got %q, want %q", got, v)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
