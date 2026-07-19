//go:build linux

package peercred

import (
	"net"

	"golang.org/x/sys/unix"
)

// FromConn reads SO_PEERCRED for the peer of a Unix socket connection.
func FromConn(conn net.Conn) (PeerCredentials, error) {
	raw, err := rawConn(conn)
	if err != nil {
		return PeerCredentials{}, err
	}
	var creds PeerCredentials
	var opErr error
	if controlErr := raw.Control(func(fd uintptr) {
		ucred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			opErr = e
			return
		}
		creds = PeerCredentials{PID: int(ucred.Pid), UID: int(ucred.Uid), GID: int(ucred.Gid)}
	}); controlErr != nil {
		return PeerCredentials{}, controlErr
	}
	return creds, opErr
}
