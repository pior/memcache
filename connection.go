package memcache

import (
	"bufio"
	"net"

	"github.com/pior/memcache/meta"
)

func NewConnection(conn net.Conn) *Connection {
	return &Connection{
		conn:   conn,
		Reader: bufio.NewReader(conn),
		Writer: bufio.NewWriter(conn),
	}
}

// Connection wraps a network connection with buffered reader and writer for efficient I/O.
type Connection struct {
	conn   net.Conn
	Reader *bufio.Reader
	Writer *bufio.Writer
}

func (c *Connection) Close() error {
	return c.conn.Close()
}

func (c *Connection) Send(req *meta.Request) (*meta.Response, error) {
	// Write request to buffered writer
	if err := meta.WriteRequest(c.Writer, req); err != nil {
		return nil, err
	}

	// Flush the buffered writer
	if err := c.Writer.Flush(); err != nil {
		return nil, err
	}

	resp, err := meta.ReadResponse(c.Reader)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
