// Package patchwidgets provides three small interactive prompt widgets —
// [Ask], [Pick], and [Confirm] — built on top of patchtui.
//
// The widgets are designed to be invoked as one-shot, blocking calls.
// Each takes a typed spec, paints itself on the controlling TTY, blocks
// until the user submits or cancels, restores the terminal, and returns
// a [Result].
//
// All widgets read keystrokes from and paint to /dev/tty, so the process
// stdin and stdout are free for piping JSON in and result JSON out. This
// makes patchwidgets suitable for use as a CLI surface invoked by hooks,
// scripts, or other agents.
//
//	res, err := patchwidgets.Confirm(patchwidgets.ConfirmSpec{
//	    Input:   "Delete branch main?",
//	    Default: "no",
//	})
//
// The package depends only on patchtui (rendering primitives) and
// golang.org/x/term (raw mode).
package patchwidgets
