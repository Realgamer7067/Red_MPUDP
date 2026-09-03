package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUnknownSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"frobnicate"}, &out, &errb)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), `unknown subcommand "frobnicate"`) {
		t.Fatalf("stderr = %q, want it to mention the unknown subcommand", errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestRunNoArgs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb.String(), "usage:") {
		t.Fatalf("stderr = %q, want usage text", errb.String())
	}
}

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var out, errb bytes.Buffer
		code := run([]string{arg}, &out, &errb)
		if code != exitOK {
			t.Fatalf("%s: exit code = %d, want %d", arg, code, exitOK)
		}
		if !strings.HasPrefix(out.String(), "red-mpudp ") {
			t.Fatalf("%s: stdout = %q, want it to start with %q", arg, out.String(), "red-mpudp ")
		}
		if errb.Len() != 0 {
			t.Fatalf("%s: stderr = %q, want empty", arg, errb.String())
		}
	}
}

func TestRunHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(out.String(), "subcommands:") {
		t.Fatalf("stdout = %q, want usage text", out.String())
	}
}
