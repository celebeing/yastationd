package logship

import (
	"net"
	"syscall"
	"time"
)

const (
	siocOutq       = 0x5411 // SIOCOUTQ = TIOCOUTQ: bytes in the send queue not yet acknowledged
	tcpUserTimeout = 18     // TCP_USER_TIMEOUT
	solTCP         = 6
)

// socketUnacked is how many bytes written to c the peer has not acknowledged (0 if unknown).
func socketUnacked(c net.Conn) int {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return 0
	}
	raw, err := tc.SyscallConn()
	if err != nil {
		return 0
	}
	n := 0
	raw.Control(func(fd uintptr) {
		v, err := ioctlInt(fd, siocOutq)
		if err == nil {
			n = v
		}
	})
	return n
}

// setUserTimeout makes a write into a dead link fail after d instead of retrying for minutes.
func setUserTimeout(c net.Conn, d time.Duration) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	if raw, err := tc.SyscallConn(); err == nil {
		raw.Control(func(fd uintptr) {
			syscall.SetsockoptInt(int(fd), solTCP, tcpUserTimeout, int(d/time.Millisecond))
		})
	}
}
