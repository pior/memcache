package memcache

import (
	"bufio"
	"fmt"
	"net"
	"testing"

	"github.com/pior/memcache/protocol"
)

func assertNoResponseError(t testing.TB, cmd ...*protocol.Command) {
	t.Helper()
	for _, c := range cmd {
		if c.Response == nil {
			t.Fatalf("Operation %s on key %s with opaque token %s failed: response is nil", c.Type, c.Key, c.Opaque)
		}
		if c.Response.Error != nil {
			t.Fatalf("Operation %s on key %s with opaque token %s failed: %v", c.Type, c.Key, c.Opaque, c.Response.Error)
		}
	}
}

func assertResponseErrorIs(t testing.TB, cmd *protocol.Command, expectedError error) {
	t.Helper()
	if cmd.Response.Error != expectedError {
		t.Fatalf("Expected error %v, got: %v", expectedError, cmd.Response.Error)
	}
}

func assertResponseStatus(t testing.TB, cmd *protocol.Command, expectedStatus string) {
	t.Helper()
	if cmd.Response == nil {
		t.Errorf("Operation %s on key %s with opaque token %s failed: response is nil", cmd.Type, cmd.Key, cmd.Opaque)
	}
	if cmd.Response.Status != expectedStatus {
		t.Errorf("Expected status %q, got %q", expectedStatus, cmd.Response.Status)
	}
}

func assertResponseValueIs(t testing.TB, cmd *protocol.Command, expectedValue []byte) {
	t.Helper()
	if cmd.Response == nil {
		t.Errorf("Operation %s on key %s with opaque token %s failed: response is nil", cmd.Type, cmd.Key, cmd.Opaque)
	}
	if cmd.Response.Error != nil {
		t.Errorf("Operation %s on key %s with opaque token %s failed: %v", cmd.Type, cmd.Key, cmd.Opaque, cmd.Response.Error)
	}
	if string(cmd.Response.Value) != string(expectedValue) {
		t.Errorf("Expected value %q, got %q", string(expectedValue), string(cmd.Response.Value))
	}
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
