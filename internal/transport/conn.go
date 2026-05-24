package transport

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// maxFrameSize caps a single frame so a malformed length prefix can't
// drive an unbounded allocation.
const maxFrameSize = 16 << 20 // 16 MiB

// Conn reads and writes Frames over a byte stream using a 4-byte
// big-endian length prefix followed by the JSON-encoded frame. It works
// over either a single io.ReadWriteCloser (a socket) or a separate
// reader/writer pair (SSH stdin/stdout).
type Conn struct {
	w  io.Writer
	c  io.Closer
	br *bufio.Reader
}

// NewConn wraps a duplex stream such as a unix socket connection.
func NewConn(rwc io.ReadWriteCloser) *Conn {
	return &Conn{w: rwc, c: rwc, br: bufio.NewReader(rwc)}
}

// NewConnRW wraps a separate reader and writer, e.g. a subprocess's
// stdout and stdin reached over SSH.
func NewConnRW(r io.Reader, w io.Writer) *Conn {
	return &Conn{w: w, br: bufio.NewReader(r)}
}

func (c *Conn) WriteFrame(f Frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(data) > maxFrameSize {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := c.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = c.w.Write(data)
	return err
}

func (c *Conn) ReadFrame() (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameSize {
		return Frame{}, fmt.Errorf("frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return Frame{}, err
	}
	var f Frame
	if err := json.Unmarshal(buf, &f); err != nil {
		return Frame{}, err
	}
	return f, nil
}

func (c *Conn) Close() error {
	if c.c != nil {
		return c.c.Close()
	}
	return nil
}
