package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
)

// TestPipeliningOpaqueMatching tests that responses are correctly matched to commands using opaque values
func TestPipeliningOpaqueMatching(t *testing.T) {
	// Start a test server that responds out of order
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Server that responds to commands out of order based on opaque values
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)

		// Read two commands
		var opaques []string

		for range 2 {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			// Extract opaque from command
			parts := strings.Split(line, " ")
			for _, part := range parts {
				if strings.HasPrefix(part, "O") {
					opaque := strings.TrimSpace(part[1:])
					opaques = append(opaques, opaque)
					break
				}
			}
		}

		// Respond in reverse order (second command first, then first command)
		if len(opaques) >= 2 {
			// Response to second command first
			response1 := "HD O" + opaques[1] + "\r\n"
			if _, err := conn.Write([]byte(response1)); err != nil {
				return
			}
			// Response to first command second
			response2 := "HD O" + opaques[0] + "\r\n"
			if _, err := conn.Write([]byte(response2)); err != nil {
				return
			}
		}

		// Keep connection open for a bit
		time.Sleep(100 * time.Millisecond)
	}()

	// Create connection
	connection, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer connection.Close()

	ctx := context.Background()

	// Create two commands
	cmd1 := NewGetCommand("key1")
	cmd2 := NewGetCommand("key2")

	// Execute both commands in a batch (pipelined)
	err = connection.ExecuteBatch(ctx, []*protocol.Command{cmd1, cmd2})
	if err != nil {
		t.Fatalf("ExecuteBatch() error = %v", err)
	}

	_ = WaitAll(ctx, cmd1, cmd2)

	if cmd1.Response.Error != nil {
		t.Errorf("cmd1.GetResponse() error = %v", cmd1.Response.Error)
	} else if cmd1.Response.Status != protocol.StatusHD {
		t.Errorf("cmd1 expected status HD, got %s", cmd1.Response.Status)
	}

	if cmd2.Response.Error != nil {
		t.Errorf("cmd2.GetResponse() error = %v", cmd2.Response.Error)
	} else if cmd2.Response.Status != protocol.StatusHD {
		t.Errorf("cmd2 expected status HD, got %s", cmd2.Response.Status)
	}
}

// TestPipeliningMultipleCommandsRandomOrder tests handling of many commands with random response order
func TestPipeliningMultipleCommandsRandomOrder(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	const numCommands = 5

	// Server that responds in a specific mixed order
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)

		var opaques []string

		// Read all commands and extract opaques
		for range numCommands {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			// Extract opaque from command
			parts := strings.Split(line, " ")
			for _, part := range parts {
				if strings.HasPrefix(part, "O") {
					opaques = append(opaques, strings.TrimSpace(part[1:]))
					break
				}
			}
		}

		// Respond in mixed order: 2, 4, 0, 3, 1
		responseOrder := []int{2, 4, 0, 3, 1}
		for _, idx := range responseOrder {
			if idx < len(opaques) {
				fmt.Fprintf(conn, "HD O%s\r\n", opaques[idx])
			}
		}
	}()

	// Create connection
	connection, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer connection.Close()

	ctx := context.Background()

	// Create multiple commands
	var commands []*protocol.Command
	for i := 0; i < numCommands; i++ {
		cmd := NewGetCommand(fmt.Sprintf("key%d", i))
		cmd.Opaque = fmt.Sprintf("opaque%d", i)
		commands = append(commands, cmd)
	}

	// Execute all commands in a batch
	err = connection.ExecuteBatch(ctx, commands)
	if err != nil {
		t.Fatalf("ExecuteBatch() error = %v", err)
	}

	// Get all responses and verify they're correct
	for i, cmd := range commands {
		if cmd.Response.Error != nil {
			t.Errorf("cmd%d.GetResponse() error = %v", i, cmd.Response.Error)
			continue
		}
		if cmd.Response.Status != protocol.StatusHD {
			t.Errorf("cmd%d expected status HD, got %s", i, cmd.Response.Status)
		}
		// The key should match the original command key
		if cmd.Response.Opaque != fmt.Sprintf("opaque%d", i) {
			t.Errorf("cmd%d expected opaque opaque%d, got %s", i, i, cmd.Response.Opaque)
		}
	}
}

// TestPipeliningConcurrentAccess tests that concurrent access to responses works correctly
func TestPipeliningConcurrentAccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	const numCommands = 10

	// Server that responds with random delay to simulate network jitter
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)

		var opaques []string

		// Read all commands
		for i := 0; i < numCommands; i++ {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			parts := strings.Split(line, " ")
			for _, part := range parts {
				if strings.HasPrefix(part, "O") {
					opaques = append(opaques, strings.TrimSpace(part[1:]))
					break
				}
			}
		}

		// Respond in reverse order with small delays
		for i := len(opaques) - 1; i >= 0; i-- {
			time.Sleep(time.Millisecond) // Small delay
			fmt.Fprintf(conn, "HD O%s\r\n", opaques[i])
		}
	}()

	connection, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer connection.Close()

	ctx := context.Background()

	// Create commands
	var commands []*protocol.Command
	for i := range numCommands {
		cmd := NewGetCommand(fmt.Sprintf("key%d", i))
		commands = append(commands, cmd)
	}
	setOpaqueFromKey(commands...)

	// Execute all commands
	err = connection.ExecuteBatch(ctx, commands)
	if err != nil {
		t.Fatalf("ExecuteBatch() error = %v", err)
	}

	_ = WaitAll(ctx, commands...)

	// Verify all responses
	for i, cmd := range commands {
		if cmd.Response.Error != nil {
			t.Errorf("cmd%d.GetResponse() error = %v", i, cmd.Response.Error)
		}
		if cmd.Response == nil {
			t.Errorf("cmd%d response is nil", i)
			continue
		}
		if cmd.Response.Status != protocol.StatusHD {
			t.Errorf("cmd%d expected status HD, got %s", i, cmd.Response.Status)
		}
		if cmd.Response.Opaque != fmt.Sprintf("opaque%d", i) {
			t.Errorf("cmd%d expected opaque opaque%d, got %s", i, i, cmd.Response.Opaque)
		}
	}
}
