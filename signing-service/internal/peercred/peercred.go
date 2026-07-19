// Package peercred reads the OS-verified credentials of the process on the other
// end of a Unix domain socket connection (SO_PEERCRED on Linux, LOCAL_PEERCRED
// on Darwin). It lets the signer enforce a UID allowlist as one of three
// independent transport controls, alongside the private socket and asymmetric
// command authentication.
package peercred

import (
	"errors"
	"net"
	"syscall"
)

// ErrUnsupported is returned on platforms without a peer-credential mechanism.
var ErrUnsupported = errors.New("peer credentials are not supported on this platform")

// PeerCredentials identifies the connecting process. GID is -1 when the platform
// does not report a single owning group.
type PeerCredentials struct {
	PID int
	UID int
	GID int
}

func rawConn(conn net.Conn) (syscall.RawConn, error) {
	sysConn, ok := conn.(syscall.Conn)
	if !ok {
		return nil, errors.New("connection does not expose a raw file descriptor")
	}
	return sysConn.SyscallConn()
}
