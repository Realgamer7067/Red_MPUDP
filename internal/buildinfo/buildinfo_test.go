package buildinfo

import "testing"

// BOOT-08: Format is deterministic when build metadata is injected.
func TestFormatDeterministic(t *testing.T) {
	in := Info{
		Version:   "v1.2.3",
		Commit:    "0123456789ab",
		Date:      "2026-09-03T12:00:00Z",
		GoVersion: "go1.27.0",
	}
	const want = "red-mpudp v1.2.3 (commit 0123456789ab, built 2026-09-03T12:00:00Z, go1.27.0)"

	if got := Format(in); got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
	// Same input, same output.
	if Format(in) != Format(in) {
		t.Fatal("Format is not deterministic for identical input")
	}
}

func TestGetFillsDefaults(t *testing.T) {
	got := Get()
	if got.Version == "" || got.Commit == "" || got.Date == "" {
		t.Fatalf("Get() left an empty field: %+v", got)
	}
	if got.GoVersion == "" {
		t.Fatal("Get() did not set GoVersion")
	}
}

func TestShortCommit(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"abcdef": "abcdef",
		"0123456789abcdef0123456789abcdef01234567": "0123456789ab",
	}
	for in, want := range cases {
		if got := shortCommit(in); got != want {
			t.Errorf("shortCommit(%q) = %q, want %q", in, got, want)
		}
	}
}
