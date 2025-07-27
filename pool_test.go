package memcache

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNewPool(t *testing.T) {
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
			// Keep connection open
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					_, err := c.Read(buf)
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	config := &PoolConfig{
		MinConnections: 2,
		MaxConnections: 5,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool, err := NewPool(addr, config)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()
	if stats.TotalConnections < config.MinConnections {
		t.Errorf("Pool should have at least %d connections, got %d", config.MinConnections, stats.TotalConnections)
	}

	if stats.MaxConnections != config.MaxConnections {
		t.Errorf("Pool MaxConnections = %d, want %d", stats.MaxConnections, config.MaxConnections)
	}

	if stats.Address != addr {
		t.Errorf("Pool Address = %s, want %s", stats.Address, addr)
	}
}

func TestNewPoolWithNilConfig(t *testing.T) {
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

	pool, err := NewPool(addr, nil)
	if err != nil {
		t.Fatalf("NewPool() with nil config error = %v", err)
	}
	defer pool.Close()

	// Should use default config
	defaultConfig := DefaultPoolConfig()
	stats := pool.Stats()

	if stats.MaxConnections != defaultConfig.MaxConnections {
		t.Errorf("Pool MaxConnections = %d, want %d", stats.MaxConnections, defaultConfig.MaxConnections)
	}
}

func TestNewPoolConnectionFailure(t *testing.T) {
	// Try to create pool to non-existent address
	config := &PoolConfig{
		MinConnections: 1,
		MaxConnections: 5,
		ConnTimeout:    10 * time.Millisecond,
		IdleTimeout:    time.Minute,
	}

	_, err := NewPool("127.0.0.1:1", config)
	if err == nil {
		t.Error("NewPool() should fail when connecting to non-existent address")
	}
}

func TestPoolGet(t *testing.T) {
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
			// Keep connection open
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					_, err := c.Read(buf)
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	config := &PoolConfig{
		MinConnections: 1,
		MaxConnections: 3,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool, err := NewPool(addr, config)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	// Get a connection
	conn1, err := pool.Get()
	if err != nil {
		t.Fatalf("Pool.Get() error = %v", err)
	}

	if conn1 == nil {
		t.Error("Pool.Get() returned nil connection")
	}

	// Get another connection - should be able to create up to max
	conn2, err := pool.Get()
	if err != nil {
		t.Fatalf("Pool.Get() second call error = %v", err)
	}

	if conn2 == nil {
		t.Error("Pool.Get() second call returned nil connection")
	}
}

func TestPoolGetAfterClose(t *testing.T) {
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

	pool, err := NewPool(addr, nil)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}

	// Close the pool
	pool.Close()

	// Try to get connection from closed pool
	_, err = pool.Get()
	if err != ErrPoolClosed {
		t.Errorf("Pool.Get() on closed pool error = %v, want %v", err, ErrPoolClosed)
	}
}

func TestPoolExecute(t *testing.T) {
	// Start a simple test server that responds with "EN\r\n" (not found)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections and send simple response
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
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if n > 0 {
						c.Write([]byte("EN\r\n")) // Not found response
					}
				}
			}(conn)
		}
	}()

	pool, err := NewPool(addr, nil)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	// Execute a command
	cmd := FormatGetCommand("test", []string{"v"}, "")
	ctx := context.Background()

	resp, err := pool.Execute(ctx, cmd)
	if err != nil {
		t.Fatalf("Pool.Execute() error = %v", err)
	}

	if resp.Status != "EN" {
		t.Errorf("Pool.Execute() response status = %s, want EN", resp.Status)
	}
}

func TestPoolExecuteBatch(t *testing.T) {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections and send responses
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
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if n > 0 {
						// Send two responses for batch
						c.Write([]byte("EN\r\nEN\r\n"))
					}
				}
			}(conn)
		}
	}()

	pool, err := NewPool(addr, nil)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	// Execute batch commands
	commands := [][]byte{
		FormatGetCommand("test1", []string{"v"}, ""),
		FormatGetCommand("test2", []string{"v"}, ""),
	}
	ctx := context.Background()

	responses, err := pool.ExecuteBatch(ctx, commands)
	if err != nil {
		t.Fatalf("Pool.ExecuteBatch() error = %v", err)
	}

	if len(responses) != 2 {
		t.Errorf("Pool.ExecuteBatch() returned %d responses, want 2", len(responses))
	}

	for i, resp := range responses {
		if resp.Status != "EN" {
			t.Errorf("Pool.ExecuteBatch() response[%d] status = %s, want EN", i, resp.Status)
		}
	}
}

func TestPoolStats(t *testing.T) {
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
			// Keep connection open
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					_, err := c.Read(buf)
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	config := &PoolConfig{
		MinConnections: 2,
		MaxConnections: 5,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool, err := NewPool(addr, config)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()

	if stats.Address != addr {
		t.Errorf("Stats.Address = %s, want %s", stats.Address, addr)
	}

	if stats.MinConnections != config.MinConnections {
		t.Errorf("Stats.MinConnections = %d, want %d", stats.MinConnections, config.MinConnections)
	}

	if stats.MaxConnections != config.MaxConnections {
		t.Errorf("Stats.MaxConnections = %d, want %d", stats.MaxConnections, config.MaxConnections)
	}

	if stats.TotalConnections < config.MinConnections {
		t.Errorf("Stats.TotalConnections = %d, want >= %d", stats.TotalConnections, config.MinConnections)
	}
}

func TestDefaultPoolConfig(t *testing.T) {
	config := DefaultPoolConfig()

	if config.MinConnections <= 0 {
		t.Error("DefaultPoolConfig MinConnections should be > 0")
	}

	if config.MaxConnections <= config.MinConnections {
		t.Error("DefaultPoolConfig MaxConnections should be > MinConnections")
	}

	if config.ConnTimeout <= 0 {
		t.Error("DefaultPoolConfig ConnTimeout should be > 0")
	}

	if config.IdleTimeout <= 0 {
		t.Error("DefaultPoolConfig IdleTimeout should be > 0")
	}
}
