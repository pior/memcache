package memcache

import (
	"fmt"
	"net"
	"os"
	"testing"
)

func TestDebug_VAndSFlags(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("SKIP_INTEGRATION is set")
	}

	// Connect directly to memcached to see raw protocol
	conn, err := net.Dial("tcp", "localhost:11211")
	if err != nil {
		t.Skipf("Cannot connect to memcached: %v", err)
	}
	defer conn.Close()

	// Set a value first using regular set command
	key := "test_debug_vs"
	value := "hello world"

	setCmd := fmt.Sprintf("set %s 0 600 %d\r\n%s\r\n", key, len(value), value)
	t.Logf("Sending: %q", setCmd)
	conn.Write([]byte(setCmd))

	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	t.Logf("Set response: %q", string(buf[:n]))

	// Now try to get with just v flag
	getCmd := fmt.Sprintf("mg %s v\r\n", key)
	t.Logf("Sending: %q", getCmd)
	conn.Write([]byte(getCmd))

	n, _ = conn.Read(buf)
	t.Logf("Get response with v only: %q", string(buf[:n]))

	// Now try to get with both v and s flags
	getCmd = fmt.Sprintf("mg %s v s\r\n", key)
	t.Logf("Sending: %q", getCmd)
	conn.Write([]byte(getCmd))

	n, _ = conn.Read(buf)
	t.Logf("Get response with v and s: %q", string(buf[:n]))
}
