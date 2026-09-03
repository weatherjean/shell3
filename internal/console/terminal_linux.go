//go:build linux

package console

import "golang.org/x/sys/unix"

const terminalGet = unix.TCGETS
const terminalSet = unix.TCSETS
