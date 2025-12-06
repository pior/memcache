package testutils

import (
	"bytes"
	"net"
	"strings"
	"time"
)

// ConnectionMock is a mock implementation of net.Conn for testing
type ConnectionMock struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
}

// NewConnectionMock creates a new mock connection with pre-configured response data
func NewConnectionMock(responseData ...string) *ConnectionMock {
	readBuf := bytes.NewBufferString(strings.Join(responseData, ""))
	return &ConnectionMock{
		readBuf:  readBuf,
		writeBuf: &bytes.Buffer{},
	}
}

func (m *ConnectionMock) Read(b []byte) (n int, err error) {
	return m.readBuf.Read(b)
}

func (m *ConnectionMock) Write(b []byte) (n int, err error) {
	return m.writeBuf.Write(b)
}

func (m *ConnectionMock) Close() error {
	m.closed = true
	return nil
}

func (m *ConnectionMock) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (m *ConnectionMock) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 11211}
}

func (m *ConnectionMock) SetDeadline(t time.Time) error      { return nil }
func (m *ConnectionMock) SetReadDeadline(t time.Time) error  { return nil }
func (m *ConnectionMock) SetWriteDeadline(t time.Time) error { return nil }

// GetWrittenRequest returns the raw request bytes written to the mock connection
func (m *ConnectionMock) GetWrittenRequest() string {
	return m.writeBuf.String()
}
