package logship

import (
	"syscall"
	"unsafe"
)

func ioctlInt(fd uintptr, req uint) (int, error) {
	var v int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(unsafe.Pointer(&v))); e != 0 {
		return 0, e
	}
	return int(v), nil
}
