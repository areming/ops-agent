package transport

import (
	"net"
	"os"
)

// Listen creates a unix domain socket listener at path. A stale socket
// file from a previous run is removed first so serve can restart
// cleanly. AF_UNIX is supported on Linux and on Windows 10+.
func Listen(path string) (net.Listener, error) {
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
	}
	return net.Listen("unix", path)
}

// Dial connects to the unix domain socket at path.
func Dial(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
