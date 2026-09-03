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
