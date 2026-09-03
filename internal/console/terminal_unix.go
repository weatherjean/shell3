//go:build darwin || linux

package console

import "golang.org/x/sys/unix"

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, terminalGet)
	return err == nil
}

func makeInputImmediate(fd int) (func() error, error) {
	original, err := unix.IoctlGetTermios(fd, terminalGet)
	if err != nil {
		return nil, err
	}
	state := *original
	state.Lflag &^= unix.ICANON | unix.ECHO
	state.Cc[unix.VMIN] = 1
	state.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, terminalSet, &state); err != nil {
		return nil, err
	}
	return func() error { return unix.IoctlSetTermios(fd, terminalSet, original) }, nil
}
