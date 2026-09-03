package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultDir is where journals live at runtime (design §11.5).
const DefaultDir = "/run/red-mpudp"

// EnsureDir creates dir (and parents) with mode 0700 and verifies it is a
// directory owned appropriately for secret-free but sensitive state
// (JOURNAL-03). It is a no-op if the directory already exists with acceptable
// permissions.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("journal: %s is not a directory", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		// Tighten a too-permissive directory rather than failing: the journal
		// files themselves are 0600, but the directory should not be listable.
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("journal: %s is mode %04o and could not be tightened: %w", dir, info.Mode().Perm(), err)
		}
	}
	return nil
}

// WriteTo writes j to path atomically: a temp file in the same directory,
// created 0600, content written, fsynced, renamed into place, then the
// directory is fsynced (JOURNAL-04..08).
func (j *Journal) WriteTo(path string) error {
	if j.Schema == 0 {
		j.Schema = SchemaVersion
	}
	if err := j.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".journal-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return fsyncDir(dir)
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// Some filesystems (e.g. older overlayfs) reject directory fsync with
		// EINVAL; the rename is still durable enough for /run on tmpfs.
		if !errors.Is(err, syscall.EINVAL) {
			return err
		}
	}
	return nil
}

// Load reads and validates a journal file (JOURNAL-09, JOURNAL-10).
func Load(path string) (*Journal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j Journal
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return nil, fmt.Errorf("journal: malformed journal at %s: %w", path, err)
	}
	if err := j.Validate(); err != nil {
		return nil, err
	}
	return &j, nil
}
