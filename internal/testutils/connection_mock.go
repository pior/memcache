package testutils

import (
	"bytes"
	"net"
	"strings"
	"time"
)

// ConnectionMock is a mock implementation of net.Conn for testing
type ConnectionMock struct {
	readBuf      *bytes.Buffer
	writeBuf     *bytes.Buffer
	responseData string // Store original response data for cycling
	cycling      bool   // Enable automatic response cycling for benchmarks
}

// NewConnectionMock creates a new mock connection with pre-configured response data
func NewConnectionMock(responseData ...string) *ConnectionMock {
	data := strings.Join(responseData, "")
	return &ConnectionMock{
		readBuf:      bytes.NewBufferString(data),
		writeBuf:     &bytes.Buffer{},
		responseData: data,
		cycling:      false,
	}
}

// EnableCycling enables automatic response cycling for benchmarks.
// When enabled, the mock will automatically reset responses when exhausted.
func (m *ConnectionMock) EnableCycling() {
	m.cycling = true
}

func (m *ConnectionMock) Read(b []byte) (n int, err error) {
	n, err = m.readBuf.Read(b)
	// If cycling is enabled and buffer is exhausted, reset it for the next iteration
	if m.cycling && m.readBuf.Len() == 0 && m.responseData != "" {
		m.readBuf.Reset()
		m.readBuf.WriteString(m.responseData)
	}
	return n, err
}

func (m *ConnectionMock) Write(b []byte) (n int, err error) {
	return m.writeBuf.Write(b)
}

func (m *ConnectionMock) Close() error {
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
