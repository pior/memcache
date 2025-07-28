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
	conn, err := NewConnection(addr, time.Second)
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

	conn, err := NewConnection(addr, time.Second)
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

	conn, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}

	// Close the connection
	conn.Close()

	// Try to execute command on closed connection
	cmd := NewGetCommand("test")
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

	conn, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}

	// Close the connection
	conn.Close()

	// Try to execute batch on closed connection
	commands := []*Command{
		NewGetCommand("test1"),
		NewGetCommand("test2"),
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

	conn, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer conn.Close()

	// Execute empty batch
	ctx := context.Background()
	responses, err := conn.ExecuteBatch(ctx, []*Command{})

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

	conn, err := NewConnection(addr, time.Second)
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
	conn, err := NewConnection(addr, time.Second)
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

func TestConnectionDeadlineHandling(t *testing.T) {
	// Start a mock memcached server that responds to commands
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Mock server that simulates memcached responses
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					// Set a short read timeout to avoid hanging
					c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					// Simple response for any command (cache miss)
					response := "EN\r\n"
					c.Write([]byte(response))
					_ = n // avoid unused variable
				}
			}(conn)
		}
	}()

	// Give the server time to start
	time.Sleep(10 * time.Millisecond)

	conn, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer conn.Close()

	t.Run("ContextWithDeadline", func(t *testing.T) {
		// Test with context that has a deadline
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Create a simple command
		command := NewGetCommand("test_key")

		// Execute should succeed and set deadline on connection
		_, err := conn.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute with deadline context failed: %v", err)
		}
	})

	t.Run("ContextWithoutDeadline", func(t *testing.T) {
		// Test with context that has no deadline
		ctx := context.Background()

		// Create a simple command
		command := NewGetCommand("test_key2")

		// Execute should succeed and clear deadline on connection
		_, err := conn.Execute(ctx, command)
		if err != nil {
			t.Fatalf("Execute without deadline context failed: %v", err)
		}
	})

	t.Run("AlternatingContexts", func(t *testing.T) {
		// Test alternating between contexts with and without deadlines
		// This simulates the real-world scenario where deadline behavior was broken

		// First use context with deadline
		ctxWithDeadline, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel1()

		command1 := NewGetCommand("test_key3")
		_, err := conn.Execute(ctxWithDeadline, command1)
		if err != nil {
			t.Fatalf("Execute with deadline context failed: %v", err)
		}

		// Then use context without deadline - this should clear the previous deadline
		ctxWithoutDeadline := context.Background()

		command2 := NewGetCommand("test_key4")
		_, err = conn.Execute(ctxWithoutDeadline, command2)
		if err != nil {
			t.Fatalf("Execute without deadline context failed: %v", err)
		}

		// Use context with deadline again
		ctxWithDeadline2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()

		command3 := NewGetCommand("test_key5")
		_, err = conn.Execute(ctxWithDeadline2, command3)
		if err != nil {
			t.Fatalf("Execute with second deadline context failed: %v", err)
		}
	})
}

func TestConnectionBatchDeadlineHandling(t *testing.T) {
	// Start a mock memcached server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Mock server
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					// Count commands and send that many responses
					commands := 0
					for i := 0; i < n-1; i++ {
						if buf[i] == '\r' && buf[i+1] == '\n' {
							commands++
						}
					}
					for i := 0; i < commands; i++ {
						c.Write([]byte("EN\r\n"))
					}
				}
			}(conn)
		}
	}()

	// Give the server time to start
	time.Sleep(10 * time.Millisecond)

	conn, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer conn.Close()

	t.Run("BatchWithDeadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		commands := []*Command{
			NewGetCommand("batch_key1"),
			NewGetCommand("batch_key2"),
		}

		_, err := conn.ExecuteBatch(ctx, commands)
		if err != nil {
			t.Fatalf("ExecuteBatch with deadline failed: %v", err)
		}
	})

	t.Run("BatchWithoutDeadline", func(t *testing.T) {
		ctx := context.Background()

		commands := []*Command{
			NewGetCommand("batch_key3"),
			NewGetCommand("batch_key4"),
		}

		_, err := conn.ExecuteBatch(ctx, commands)
		if err != nil {
			t.Fatalf("ExecuteBatch without deadline failed: %v", err)
		}
	})
}
