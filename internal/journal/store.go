package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultDir is where journals live at runtime (design §11.5).
const DefaultDir = "/run/red-mpudp"

// acceptableOwner reports whether uid may own journal state for the current
// process: either the effective UID or root (the daemon runs as root and may
// later drop privileges).
func acceptableOwner(uid uint32) bool {
	return int(uid) == os.Geteuid() || uid == 0
}

// EnsureDir creates dir with mode 0700 and verifies the final component is a
// real directory (not a symlink) owned by the current user or root (JOURNAL-03).
// A mismatch is refused rather than "fixed".
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("journal: %s is a symlink; refusing to use it for state", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("journal: %s is not a directory", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("journal: cannot determine ownership of %s", dir)
	}
	if !acceptableOwner(st.Uid) {
		return fmt.Errorf("journal: %s is owned by uid %d, not the current user or root", dir, st.Uid)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("journal: %s is mode %04o and could not be tightened: %w", dir, fi.Mode().Perm(), err)
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

// Load reads and validates a journal file. The file is opened O_NOFOLLOW and,
// from the resulting descriptor, required to be a regular file with a single
// hard link, mode 0600 or tighter, owned by the current user or root — so a
// symlink, a hard link planted by another user, or a group/world-writable file
// cannot steer recovery (JOURNAL-09, JOURNAL-10, JOURNAL-23).
func Load(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("journal: %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("journal: %s is mode %04o; journal files must be 0600 or tighter", path, fi.Mode().Perm())
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("journal: cannot determine ownership of the journal file")
	}
	if st.Nlink != 1 {
		return nil, fmt.Errorf("journal: %s has %d hard links; refusing", path, st.Nlink)
	}
	if !acceptableOwner(st.Uid) {
		return nil, fmt.Errorf("journal: %s is owned by uid %d, not the current user or root", path, st.Uid)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	var j Journal
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return nil, fmt.Errorf("journal: malformed journal at %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("journal: %s has trailing data after the JSON document", path)
	}
	if err := j.Validate(); err != nil {
		return nil, err
	}
	return &j, nil
}
