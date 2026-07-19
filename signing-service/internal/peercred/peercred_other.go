//go:build !linux && !darwin

package peercred

import "net"

// FromConn reports that peer credentials are unavailable on this platform.
func FromConn(conn net.Conn) (PeerCredentials, error) {
	return PeerCredentials{}, ErrUnsupported
}
