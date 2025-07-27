package memcache

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNewConnection(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Test creating connection
	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer conn.Close()

	if conn.Addr() != addr {
		t.Errorf("Connection.Addr() = %v, want %v", conn.Addr(), addr)
	}

	if conn.IsClosed() {
		t.Error("New connection should not be closed")
	}

	if conn.InFlight() != 0 {
		t.Errorf("New connection InFlight() = %v, want 0", conn.InFlight())
	}
}

func TestNewConnectionWithTimeout(t *testing.T) {
	// Test connection to non-existent address with short timeout
	_, err := NewConnectionWithTimeout("127.0.0.1:1", 10*time.Millisecond)
	if err == nil {
		t.Error("Expected connection to fail to non-existent address")
	}
}

func TestConnectionClose(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}

	// Test that connection is not closed initially
	if conn.IsClosed() {
		t.Error("New connection should not be closed")
	}

	// Close the connection
	err = conn.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Test that connection is now closed
	if !conn.IsClosed() {
		t.Error("Connection should be closed after Close()")
	}

	// Test that closing again doesn't error
	err = conn.Close()
	if err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

func TestConnectionExecuteOnClosedConnection(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}

	// Close the connection
	conn.Close()

	// Try to execute command on closed connection
	cmd := FormatGetCommand("test", []string{"v"}, "")
	ctx := context.Background()

	_, err = conn.Execute(ctx, cmd)
	if err != ErrConnectionClosed {
		t.Errorf("Execute() on closed connection error = %v, want %v", err, ErrConnectionClosed)
	}
}

func TestConnectionExecuteBatchOnClosedConnection(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}

	// Close the connection
	conn.Close()

	// Try to execute batch on closed connection
	commands := [][]byte{
		FormatGetCommand("test1", []string{"v"}, ""),
		FormatGetCommand("test2", []string{"v"}, ""),
	}
	ctx := context.Background()

	_, err = conn.ExecuteBatch(ctx, commands)
	if err != ErrConnectionClosed {
		t.Errorf("ExecuteBatch() on closed connection error = %v, want %v", err, ErrConnectionClosed)
	}
}

func TestConnectionExecuteBatchEmptyCommands(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer conn.Close()

	// Execute empty batch
	ctx := context.Background()
	responses, err := conn.ExecuteBatch(ctx, [][]byte{})

	if err != nil {
		t.Errorf("ExecuteBatch() with empty commands error = %v", err)
	}

	if responses != nil {
		t.Errorf("ExecuteBatch() with empty commands should return nil responses")
	}
}

func TestConnectionPing(t *testing.T) {
	// We can't easily test ping without a real memcached server
	// but we can test that it fails appropriately with a closed connection

	// Start a simple test server that immediately closes connections
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept and immediately close connections
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}

	ctx := context.Background()

	// Ping should fail because server closes connection
	err = conn.Ping(ctx)
	if err == nil {
		t.Error("Ping() should fail when server closes connection")
	}

	// Connection should be marked as closed after failed ping
	if !conn.IsClosed() {
		t.Error("Connection should be closed after failed ping")
	}
}

func TestConnectionLastUsed(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	before := time.Now()
	conn, err := NewConnection(addr)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer conn.Close()
	after := time.Now()

	lastUsed := conn.LastUsed()
	if lastUsed.Before(before) || lastUsed.After(after) {
		t.Errorf("LastUsed() = %v, want between %v and %v", lastUsed, before, after)
	}
}
