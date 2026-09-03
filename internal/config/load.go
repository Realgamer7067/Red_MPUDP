package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/goccy/go-yaml"
)

// strictOptions rejects unknown fields (CONF-06). Duplicate map keys (CONF-07)
// are rejected by goccy/go-yaml by default unless AllowDuplicateMapKey is
// passed, which it is not.
func strictOptions() []yaml.DecodeOption {
	return []yaml.DecodeOption{
		yaml.DisallowUnknownField(),
	}
}

func decodeStrict(data []byte, dst any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data), strictOptions()...)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("configuration is empty")
		}
		return err
	}
	// A second Decode must hit EOF: multiple YAML documents are not allowed.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("configuration must contain exactly one YAML document")
	}
	return nil
}

// LoadClient parses and validates a client configuration from YAML bytes.
// Defaults are applied before validation. It performs no filesystem or network
// access (CONF-30); referenced key files are opened later by the identity
// package.
func LoadClient(data []byte) (*Client, error) {
	var c Client
	if err := decodeStrict(data, &c); err != nil {
		return nil, fmt.Errorf("parse client config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid client config: %w", err)
	}
	return &c, nil
}

// LoadServer parses and validates a server configuration from YAML bytes.
func LoadServer(data []byte) (*Server, error) {
	var s Server
	if err := decodeStrict(data, &s); err != nil {
		return nil, fmt.Errorf("parse server config: %w", err)
	}
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server config: %w", err)
	}
	return &s, nil
}

// LoadClientFile reads path and delegates to LoadClient.
func LoadClientFile(path string) (*Client, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadClient(data)
}

// LoadServerFile reads path and delegates to LoadServer.
func LoadServerFile(path string) (*Server, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadServer(data)
}
