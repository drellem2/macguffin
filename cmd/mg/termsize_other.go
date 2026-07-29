//go:build !unix

package main

// terminalWidth reports "unknown" everywhere the TIOCGWINSZ ioctl is not
// available. Callers fall back to listWidthDefault, so listings stay bounded
// even on a platform mg cannot measure.
func terminalWidth(fd uintptr) int {
	return 0
}
