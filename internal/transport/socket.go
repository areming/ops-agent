package transport

import (
	"net"
	"os"
)

// Listen creates a unix domain socket listener at path. A stale socket
// file from a previous run is removed first so serve can restart
// cleanly. AF_UNIX is supported on Linux and on Windows 10+.
//
// The socket is set to 0660 so a member of the agent's group (the operator,
// added by enroll) can connect, while everyone else is shut out. On Windows
// the chmod is a no-op.
func Listen(path string) (net.Listener, error) {
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// Dial connects to the unix domain socket at path.
func Dial(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
