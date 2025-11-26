package memcache

import (
	"bufio"
	"net"

	"github.com/pior/memcache/meta"
)

// Connection wraps a network connection with a buffered reader for efficient response parsing.
type Connection struct {
	net.Conn
	remoteAddr net.Addr
	Reader     *bufio.Reader
}

func (c *Connection) Send(req *meta.Request) (*meta.Response, error) {
	if err := meta.WriteRequest(c, req); err != nil {
		return nil, err
	}

	resp, err := meta.ReadResponse(c.Reader)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Connection) RemoteAddr() net.Addr {
	return c.remoteAddr
}
