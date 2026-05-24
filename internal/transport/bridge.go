package transport

import (
	"io"
	"os"
)

// Bridge copies bytes between this process's stdio and the agent's unix
// socket at path. It runs on the remote host (invoked as `opsagent
// _bridge` over SSH) so the local CLI's SSH stdin/stdout is wired
// straight through to the agent socket. It returns when either
// direction closes.
func Bridge(path string) error {
	conn, err := Dial(path)
	if err != nil {
		return err
	}
	defer conn.Close()

	errc := make(chan error, 2)
	go func() { _, err := io.Copy(conn, os.Stdin); errc <- err }()
	go func() { _, err := io.Copy(os.Stdout, conn); errc <- err }()
	return <-errc
}
