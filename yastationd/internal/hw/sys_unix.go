//go:build !windows

package hw

import "syscall"

// sys3 is a three-argument system call; the hardware paths run on Linux only, but host tools
// that import this package (cmd/intercom-peer through config) also build for Windows.
func sys3(trap, a1, a2, a3 uintptr) syscall.Errno {
	_, _, e := syscall.Syscall(trap, a1, a2, a3)
	return e
}

const (
	sysIoctl = syscall.SYS_IOCTL
	sysFcntl = syscall.SYS_FCNTL
)
