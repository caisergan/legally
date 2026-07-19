//go:build darwin

package peercred

import (
	"net"

	"golang.org/x/sys/unix"
)

// FromConn reads LOCAL_PEERCRED for the peer of a Unix socket connection. Darwin
// reports the effective uid but not a single owning gid, so GID is -1.
func FromConn(conn net.Conn) (PeerCredentials, error) {
	raw, err := rawConn(conn)
	if err != nil {
		return PeerCredentials{}, err
	}
	var creds PeerCredentials
	var opErr error
	if controlErr := raw.Control(func(fd uintptr) {
		xucred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			opErr = e
			return
		}
		creds = PeerCredentials{PID: -1, UID: int(xucred.Uid), GID: -1}
	}); controlErr != nil {
		return PeerCredentials{}, controlErr
	}
	return creds, opErr
}
