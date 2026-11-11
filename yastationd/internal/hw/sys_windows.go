package hw

import "syscall"

// sys3: no such calls on Windows (see sys_unix.go).
func sys3(trap, a1, a2, a3 uintptr) syscall.Errno { return syscall.EWINDOWS }

const (
	sysIoctl = 0
	sysFcntl = 0
)
