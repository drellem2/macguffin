//go:build unix

package main

import (
	"syscall"
	"unsafe"
)

// terminalWidth returns the column count of the terminal behind fd, or 0 if fd
// is not a terminal or the kernel will not say. It is a direct TIOCGWINSZ
// ioctl rather than a dependency: mg's only non-stdlib import is cobra, and a
// window-size probe is not worth widening that.
func terminalWidth(fd uintptr) int {
	var ws struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
