//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

// isTerminal reports whether fd is a terminal. Ask mode uses this so a
// piped one-shot does not block forever waiting for y/n.
func isTerminal(fd int) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return errno == 0
}
