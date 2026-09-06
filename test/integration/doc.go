//go:build linux && integration

// Package integration hosts the privileged network integration harness for
// RED_MPUDP (plan M05). Everything here needs Linux, effective CAP_NET_ADMIN,
// and the ip/tc/nft tools; without them every test in the package skips with a
// single precise reason (see preflight_linux_test.go).
//
// The harness builds a three-namespace topology — client-ns, server-ns,
// internet-ns — joined by two client<->server veth "paths" and one
// server<->internet uplink, then exposes deterministic tc netem impairment and
// link/address/gateway mutation helpers. Every namespace it creates is torn
// down on both success and failure, and the suite reports any leak.
package integration
