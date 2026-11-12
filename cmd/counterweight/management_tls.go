package main

import (
	"crypto/tls"
	"fmt"
	"net"
)

const defaultManagementAddress = "127.0.0.1:8787"

// Pin an HTTP hostname to the address that was checked. A subsequent DNS
// resolution must not turn an approved loopback listener into a remote one.
func managementAddress(address string, config *tls.Config) (string, error) {
	resolved, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return "", fmt.Errorf("invalid management listen address: %w", err)
	}
	if config != nil {
		return address, nil
	}
	if !resolved.IP.IsLoopback() {
		return "", fmt.Errorf("management HTTP requires a loopback address; use -tls-cert and -tls-key for network listeners")
	}
	return resolved.String(), nil
}

func managementTLS(certPath, keyPath string) (*tls.Config, error) {
	if certPath == "" && keyPath == "" {
		return nil, nil
	}
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("both -tls-cert and -tls-key are required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load management TLS certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}
