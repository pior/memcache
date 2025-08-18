package memcache

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

// TestPipeliningOpaqueMatching tests that responses are correctly matched to commands using opaque values
func TestPipeliningOpaqueMatching(t *testing.T) {
	addr := createListener(t, func(conn net.Conn) {
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
	})

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

	_ = cmd1.Wait(ctx)
	_ = cmd2.Wait(ctx)

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

func TestPipeliningMultipleCommandsRandomOrder(t *testing.T) {
	ctx := t.Context()

	const numCommands = 5

	// Create multiple commands
	var commands []*protocol.Command
	for i := range numCommands {
		commands = append(commands, NewGetCommand(fmt.Sprintf("key%d", i)))
	}
	setOpaqueFromKey(commands...)

	// Store the received command lines
	var receivedComands []string

	addr := createListener(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)

		// Read all commands
		for range numCommands {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			receivedComands = append(receivedComands, line)
		}

		// Write the responses in random order
		responseOrder := []int{0, 1, 2, 3, 4}
		rand.Shuffle(numCommands, func(i, j int) {
			responseOrder[i], responseOrder[j] = responseOrder[j], responseOrder[i]
		})
		for _, idx := range responseOrder {
			fmt.Fprintf(conn, "HD O%s\r\n", commands[idx].Opaque)
		}
	})

	connection, err := NewConnection(addr, time.Second)
	require.NoError(t, err)
	defer connection.Close()

	// Execute all commands in a batch
	err = connection.ExecuteBatch(ctx, commands)
	require.NoError(t, err)

	WaitAll(ctx, commands...)

	expectedCommands := []string{
		"mg key0 v Okey0\r\n",
		"mg key1 v Okey1\r\n",
		"mg key2 v Okey2\r\n",
		"mg key3 v Okey3\r\n",
		"mg key4 v Okey4\r\n",
	}

	if !slices.Equal(receivedComands, expectedCommands) {
		t.Fatalf("Received commands do not match expected order: got %v, want %v", receivedComands, expectedCommands)
	}

	for i, cmd := range commands {
		assertResponseStatus(t, cmd, protocol.StatusHD)

		if cmd.Response.Opaque != cmd.Opaque {
			t.Errorf("cmd%d expected opaque %s, got %s", i, cmd.Opaque, cmd.Response.Opaque)
		}
	}
}
