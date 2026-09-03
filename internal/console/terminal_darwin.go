//go:build darwin

package console

import "golang.org/x/sys/unix"

const terminalGet = unix.TIOCGETA
const terminalSet = unix.TIOCSETA
