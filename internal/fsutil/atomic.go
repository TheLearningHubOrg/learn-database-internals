// Package fsutil holds the file-system durability helpers that every storage
// engine ends up writing.
package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
)

// WriteFileAtomic replaces path with data so that after a crash the file holds
// either the old contents or the new contents, never a mix.
//
//  1. write a temporary file in the same directory
//  2. fsync it, so its data is on stable storage
//  3. rename it over the target, which is atomic on POSIX file systems
//  4. fsync the directory, so the rename itself survives a crash
//
// Engines use this for small metadata files: RocksDB's CURRENT file, a
// checkpoint pointer, a manifest.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return SyncDir(dir)
}

// SyncDir fsyncs a directory so that file creations, renames and deletions
// inside it are durable. It is a no-op on Windows, which cannot fsync a
// directory handle.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
