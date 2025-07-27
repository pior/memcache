package memcache

import (
	"net"
	"testing"
)

func TestNewConsistentHashSelector(t *testing.T) {
	selector := NewConsistentHashSelector()

	if selector.virtualNodes != 150 {
		t.Errorf("Default virtual nodes = %d, want 150", selector.virtualNodes)
	}

	servers := selector.GetServers()
	if len(servers) != 0 {
		t.Errorf("New selector should have 0 servers, got %d", len(servers))
	}
}

func TestNewConsistentHashSelectorWithVirtualNodes(t *testing.T) {
	virtualNodes := 200
	selector := NewConsistentHashSelectorWithVirtualNodes(virtualNodes)

	if selector.virtualNodes != virtualNodes {
		t.Errorf("Virtual nodes = %d, want %d", selector.virtualNodes, virtualNodes)
	}
}

func TestConsistentHashSelectorSelectServerNoServers(t *testing.T) {
	selector := NewConsistentHashSelector()

	_, err := selector.SelectServer("test")
	if err != ErrNoServersAvailable {
		t.Errorf("SelectServer() error = %v, want %v", err, ErrNoServersAvailable)
	}
}

func TestConsistentHashSelectorAddServer(t *testing.T) {
	// Start test servers
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}
	defer listener1.Close()

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer listener2.Close()

	addr1 := listener1.Addr().String()
	addr2 := listener2.Addr().String()

	// Accept connections in background
	go acceptConnections(listener1)
	go acceptConnections(listener2)

	// Create pools
	pool1, err := NewPool(addr1, nil)
	if err != nil {
		t.Fatalf("NewPool(addr1) error = %v", err)
	}
	defer pool1.Close()

	pool2, err := NewPool(addr2, nil)
	if err != nil {
		t.Fatalf("NewPool(addr2) error = %v", err)
	}
	defer pool2.Close()

	selector := NewConsistentHashSelector()

	// Add servers
	selector.AddServer(addr1, pool1)
	selector.AddServer(addr2, pool2)

	servers := selector.GetServers()
	if len(servers) != 2 {
		t.Errorf("Selector should have 2 servers, got %d", len(servers))
	}

	// Test that we can select a server
	selectedPool, err := selector.SelectServer("test")
	if err != nil {
		t.Fatalf("SelectServer() error = %v", err)
	}

	if selectedPool == nil {
		t.Error("SelectServer() returned nil pool")
	}
}

func TestConsistentHashSelectorSingleServer(t *testing.T) {
	// Start test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	go acceptConnections(listener)

	// Create pool
	pool, err := NewPool(addr, nil)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	selector := NewConsistentHashSelector()
	selector.AddServer(addr, pool)

	// With only one server, all keys should go to it
	for _, key := range []string{"key1", "key2", "key3"} {
		selectedPool, err := selector.SelectServer(key)
		if err != nil {
			t.Fatalf("SelectServer(%s) error = %v", key, err)
		}

		if selectedPool != pool {
			t.Errorf("SelectServer(%s) returned wrong pool", key)
		}
	}
}

func TestConsistentHashSelectorRemoveServer(t *testing.T) {
	// Start test servers
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}
	defer listener1.Close()

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer listener2.Close()

	addr1 := listener1.Addr().String()
	addr2 := listener2.Addr().String()

	go acceptConnections(listener1)
	go acceptConnections(listener2)

	// Create pools
	pool1, err := NewPool(addr1, nil)
	if err != nil {
		t.Fatalf("NewPool(addr1) error = %v", err)
	}
	defer pool1.Close()

	pool2, err := NewPool(addr2, nil)
	if err != nil {
		t.Fatalf("NewPool(addr2) error = %v", err)
	}
	defer pool2.Close()

	selector := NewConsistentHashSelector()
	selector.AddServer(addr1, pool1)
	selector.AddServer(addr2, pool2)

	// Remove one server
	selector.RemoveServer(addr1)

	servers := selector.GetServers()
	if len(servers) != 1 {
		t.Errorf("Selector should have 1 server after removal, got %d", len(servers))
	}

	// All keys should now go to the remaining server
	selectedPool, err := selector.SelectServer("test")
	if err != nil {
		t.Fatalf("SelectServer() error = %v", err)
	}

	if selectedPool != pool2 {
		t.Error("SelectServer() should return pool2 after removing pool1")
	}
}

func TestConsistentHashSelectorClose(t *testing.T) {
	// Start test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	go acceptConnections(listener)

	// Create pool
	pool, err := NewPool(addr, nil)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}

	selector := NewConsistentHashSelector()
	selector.AddServer(addr, pool)

	// Close selector
	err = selector.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Should have no servers after close
	servers := selector.GetServers()
	if len(servers) != 0 {
		t.Errorf("Selector should have 0 servers after close, got %d", len(servers))
	}
}

func TestConsistentHashSelectorConsistency(t *testing.T) {
	// Start test servers
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}
	defer listener1.Close()

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer listener2.Close()

	addr1 := listener1.Addr().String()
	addr2 := listener2.Addr().String()

	go acceptConnections(listener1)
	go acceptConnections(listener2)

	// Create pools
	pool1, err := NewPool(addr1, nil)
	if err != nil {
		t.Fatalf("NewPool(addr1) error = %v", err)
	}
	defer pool1.Close()

	pool2, err := NewPool(addr2, nil)
	if err != nil {
		t.Fatalf("NewPool(addr2) error = %v", err)
	}
	defer pool2.Close()

	selector := NewConsistentHashSelector()
	selector.AddServer(addr1, pool1)
	selector.AddServer(addr2, pool2)

	// Test that the same key always goes to the same server
	testKey := "consistent_test_key"

	firstSelection, err := selector.SelectServer(testKey)
	if err != nil {
		t.Fatalf("SelectServer() error = %v", err)
	}

	// Repeat the selection multiple times
	for i := 0; i < 10; i++ {
		selectedPool, err := selector.SelectServer(testKey)
		if err != nil {
			t.Fatalf("SelectServer() iteration %d error = %v", i, err)
		}

		if selectedPool != firstSelection {
			t.Errorf("SelectServer() inconsistent: iteration %d returned different pool", i)
		}
	}
}

// Helper function to accept connections in background
func acceptConnections(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Keep connection open but don't do anything with it
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
}
