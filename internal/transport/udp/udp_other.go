//go:build !linux

// Package udp has no implementation outside Linux: the sockets depend on
// SO_BINDTODEVICE, SO_MARK, IP_PKTINFO and IP_RECVERR.
package udp

import "github.com/Realgamer7067/Red_MPUDP/internal/transport"

// ClientConfig is declared so callers compile everywhere.
type ClientConfig struct{}

// ServerConfig is declared so callers compile everywhere.
type ServerConfig struct{}

// Dial always returns transport.ErrUnsupported.
func Dial(ClientConfig) (transport.DatagramIO, error) { return nil, transport.ErrUnsupported }

// Listen always returns transport.ErrUnsupported.
func Listen(ServerConfig) (transport.DatagramIO, error) { return nil, transport.ErrUnsupported }
