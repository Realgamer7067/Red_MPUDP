package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteToIsAtomicAnd0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.journal")

	j := validJournal()
	if err := j.WriteTo(path); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode = %04o, want 0600", info.Mode().Perm())
	}

	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory has %d entries after write: %v", len(entries), names)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, j) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, j)
	}
}

func TestWriteToRejectsInvalidJournal(t *testing.T) {
	dir := t.TempDir()
	j := validJournal()
	j.Role = "bogus"
	if err := j.WriteTo(filepath.Join(dir, "x")); err == nil {
		t.Fatal("WriteTo wrote an invalid journal")
	}
}

func TestLoadRejects(t *testing.T) {
	dir := t.TempDir()

	// Missing file.
	if _, err := Load(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("Load of missing file succeeded")
	}

	// Malformed JSON (JOURNAL-23).
	p := filepath.Join(dir, "bad.json")
	os.WriteFile(p, []byte("{ not json"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("Load of malformed JSON succeeded")
	}

	// Unknown field.
	p2 := filepath.Join(dir, "unknown.json")
	os.WriteFile(p2, []byte(`{"schema":1,"role":"client","instance_id":"red-mpudp-01","surprise":true}`), 0o600)
	if _, err := Load(p2); err == nil {
		t.Fatal("Load accepted an unknown field")
	}

	// Wrong schema version (JOURNAL-09).
	p3 := filepath.Join(dir, "v2.json")
	os.WriteFile(p3, []byte(`{"schema":2,"role":"client","instance_id":"red-mpudp-01"}`), 0o600)
	if _, err := Load(p3); err == nil {
		t.Fatal("Load accepted schema version 2")
	}

	// Trailing data after the JSON document.
	p4 := filepath.Join(dir, "trailing.json")
	os.WriteFile(p4, []byte(`{"schema":1,"role":"client","instance_id":"red-mpudp-01"}`+"\n{}"), 0o600)
	if _, err := Load(p4); err == nil {
		t.Fatal("Load accepted trailing data after the JSON document")
	}
}

// Blocker 8: Load hardening — group/world-accessible files, symlinks, and
// hard-linked files are refused before the content is parsed.
func TestLoadFilePermissionAndTypeChecks(t *testing.T) {
	dir := t.TempDir()
	good := []byte(`{"schema":1,"role":"client","instance_id":"red-mpudp-good01"}`)

	// Group-readable.
	loose := filepath.Join(dir, "loose.journal")
	if err := os.WriteFile(loose, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(loose); err == nil {
		t.Fatal("Load accepted a group-readable journal")
	}

	// Symlink to a valid file.
	real := filepath.Join(dir, "real.journal")
	os.WriteFile(real, good, 0o600)
	link := filepath.Join(dir, "link.journal")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("Load followed a symlink")
	}

	// Hard link (nlink > 1).
	hard := filepath.Join(dir, "hard.journal")
	if err := os.Link(real, hard); err != nil {
		t.Skipf("hard link not supported: %v", err)
	}
	if _, err := Load(hard); err == nil {
		t.Fatal("Load accepted a file with multiple hard links")
	}
}

// Blocker 7: EnsureDir refuses a symlinked directory rather than using it.
func TestEnsureDirRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	os.Mkdir(realDir, 0o700)
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if err := EnsureDir(link); err == nil {
		t.Fatal("EnsureDir accepted a symlinked directory")
	}
}

func TestEnsureDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "run", "red-mpudp")
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %04o, want 0700", info.Mode().Perm())
	}
	// Idempotent.
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("second EnsureDir: %v", err)
	}

	// Tightens a loose directory.
	loose := filepath.Join(base, "loose")
	os.Mkdir(loose, 0o755)
	if err := EnsureDir(loose); err != nil {
		t.Fatalf("EnsureDir on loose dir: %v", err)
	}
	li, _ := os.Stat(loose)
	if li.Mode().Perm()&0o077 != 0 {
		t.Fatalf("loose dir not tightened: %04o", li.Mode().Perm())
	}
}
