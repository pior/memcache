package memcache

import (
	"bufio"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

// TestPipeliningOpaqueMatching tests that responses are correctly matched to commands using opaque values
func TestPipeliningOpaqueMatching(t *testing.T) {
	addr := createListener(t, func(conn *bufio.ReadWriter) {
		line, err := conn.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "mg key2 v k O1\r\n", line)

		conn.WriteString("HD O1 kkey1\r\n")

		line, err = conn.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "mg key2 v k O2\r\n", line)

		conn.WriteString("HD O2 kkey2\r\n")
		conn.Flush()
	})

	connection, err := NewConnection(addr, time.Second)
	if err != nil {
		t.Fatalf("NewConnection() error = %v", err)
	}
	defer connection.Close()

	ctx := t.Context()

	// Create two commands
	cmd1 := NewGetCommand("key1")
	cmd2 := NewGetCommand("key2")

	// Execute both commands in a batch (pipelined)
	err = connection.Execute(ctx, cmd1, cmd2)
	if err != nil {
		t.Fatalf("ExecuteBatch() error = %v", err)
	}

	_ = cmd1.Wait(ctx)
	_ = cmd2.Wait(ctx)

	assertNoResponseError(t, cmd1)
	assertResponseStatus(t, cmd1, protocol.StatusHD)

	assertNoResponseError(t, cmd2)
	assertResponseStatus(t, cmd2, protocol.StatusHD)
}
