// Package socket provides a minimal line-oriented Unix-domain-socket transport:
// a listener that invokes a handler per newline-delimited message, and a Send
// that connects, writes one message, and closes. A failed Send (ENOENT /
// ECONNREFUSED) doubles as the liveness signal that an endpoint is gone.
package socket

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

// Listener wraps the accept loop so callers can Close to stop it and remove the
// socket file.
type Listener struct {
	l    net.Listener
	path string
}

// Listen creates (replacing any stale file) a Unix-domain socket at path and
// invokes handler for each newline-delimited message received. macOS caps the
// socket path at ~104 bytes — keep path short (a numeric session id).
func Listen(path string, handler func(line []byte)) (*Listener, error) {
	_ = os.Remove(path) // clear a stale socket from an unclean prior exit
	if err := os.MkdirAll(dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("socket: mkdir: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("socket: listen %s: %w", path, err)
	}
	ls := &Listener{l: l, path: path}
	go ls.accept(handler)
	return ls, nil
}

func (ls *Listener) accept(handler func([]byte)) {
	for {
		conn, err := ls.l.Accept()
		if err != nil {
			return // listener closed
		}
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				b := append([]byte(nil), sc.Bytes()...)
				handler(b)
			}
		}(conn)
	}
}

// Close stops the accept loop and removes the socket file.
func (ls *Listener) Close() error {
	err := ls.l.Close()
	_ = os.Remove(ls.path)
	return err
}

// Send connects to a listening socket, writes one newline-terminated message,
// and closes. Returns an error if the socket is absent or refusing — which the
// caller treats as "endpoint dormant".
func Send(path string, msg []byte) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return fmt.Errorf("socket: dial %s: %w", path, err)
	}
	defer conn.Close()
	if len(msg) == 0 || msg[len(msg)-1] != '\n' {
		msg = append(msg, '\n')
	}
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("socket: write: %w", err)
	}
	return nil
}

func dir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
