package main

import (
	"fmt"
	"io"

	"github.com/Realgamer7067/Red_MPUDP/internal/identity"
)

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	path, ok := singlePathArg("keygen", args, stderr)
	if !ok {
		return exitUsage
	}
	priv, err := identity.GeneratePrivateKey()
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitRuntime
	}
	if err := identity.WritePrivateKeyFile(path, priv); err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitRuntime
	}
	pub, err := identity.PublicKey(priv)
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "wrote private key to %s\npublic key: %s\n", path, trimNL(identity.Encode(pub)))
	return exitOK
}

func cmdPSK(args []string, stdout, stderr io.Writer) int {
	path, ok := singlePathArg("psk", args, stderr)
	if !ok {
		return exitUsage
	}
	psk, err := identity.GeneratePSK()
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitRuntime
	}
	if err := identity.WritePrivateKeyFile(path, psk); err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "wrote pre-shared key to %s\n", path)
	return exitOK
}

func cmdPublicKey(args []string, stdout, stderr io.Writer) int {
	path, ok := singlePathArg("public-key", args, stderr)
	if !ok {
		return exitUsage
	}
	priv, err := identity.LoadPrivateKeyFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitPermission
	}
	pub, err := identity.PublicKey(priv)
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintln(stdout, trimNL(identity.Encode(pub)))
	return exitOK
}

// singlePathArg requires exactly one non-flag argument: a filesystem path
// (CLI-02, explicit output path).
func singlePathArg(name string, args []string, stderr io.Writer) (string, bool) {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintf(stderr, "red-mpudp: %s requires exactly one path argument\n", name)
		return "", false
	}
	return args[0], true
}

func trimNL(b []byte) string {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return string(b)
}
