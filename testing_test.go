package memcache

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func assertNoResponseError(t testing.TB, cmd ...*protocol.Command) {
	t.Helper()
	for _, c := range cmd {
		require.NotNil(t, c.Response, "Response should not be nil")
		require.NoError(t, c.Response.Error, "Response should not contain an error")
	}
}

func assertResponseErrorIs(t testing.TB, cmd *protocol.Command, expectedError error) {
	t.Helper()
	require.ErrorIs(t, cmd.Response.Error, expectedError, "Expected error does not match actual error")
}

func assertResponseStatus(t testing.TB, cmd *protocol.Command, expectedStatus protocol.StatusType) {
	t.Helper()
	require.NotNil(t, cmd.Response, "Response should not be nil")
	require.Equal(t, expectedStatus, cmd.Response.Status, "Response status does not match expected status")
}

func assertResponseValue(t testing.TB, cmd *protocol.Command, expectedValue []byte) {
	t.Helper()
	require.NotNil(t, cmd.Response, "Response should not be nil")
	require.NoError(t, cmd.Response.Error, "Response should not contain an error")
	require.Equal(t, string(expectedValue), string(cmd.Response.Value), "Response value does not match expected value")
}

func assertResponseValueMatch(t testing.TB, cmd *protocol.Command, valueRegexp string) {
	t.Helper()
	require.NotNil(t, cmd.Response, "Response should not be nil")
	require.NoError(t, cmd.Response.Error, "Response should not contain an error")
	require.Regexp(t, valueRegexp, string(cmd.Response.Value), "Response value does not match expected pattern")
}

func setOpaqueFromKey(cmds ...*protocol.Command) {
	for _, cmd := range cmds {
		cmd.Opaque = cmd.Key
	}
}

func createListener(t testing.TB, handler func(conn net.Conn)) string {
	// Start a simple test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}

	t.Cleanup(func() {
		listener.Close()
	})

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(c net.Conn) {
				defer c.Close()

				if handler != nil {
					handler(c)
				}
			}(conn)
		}
	}()

	// Give the server time to start
	time.Sleep(10 * time.Millisecond)

	return listener.Addr().String()
}

func statusResponder(status string) func(conn net.Conn) {
	return func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Printf("Received command: %s", line)

		_, _ = conn.Write([]byte(status))
	}
}
