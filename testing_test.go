package memcache

import (
	"os"
	"testing"

	"github.com/pior/memcache/protocol"
)

func assertNoResponseError(t *testing.T, cmd ...*protocol.Command) {
	t.Helper()
	for _, c := range cmd {
		if c.Response.Error != nil {
			t.Errorf("Operation %s on key %s with opaque token %s failed: %v", c.Type, c.Key, c.Opaque, c.Response.Error)
		}
	}
}

func assertResponseErrorIs(t *testing.T, cmd *protocol.Command, expectedError error) {
	t.Helper()
	if cmd.Response.Error != expectedError {
		t.Errorf("Expected error %v, got: %v", expectedError, cmd.Response.Error)
	}
}

func GetMemcacheServers() []string {
	return []string{os.Getenv("MEMCACHE_SERVERS")}
}

func withClient(t testing.TB) (*Client, func()) {
	servers := []string{os.Getenv("MEMCACHE_SERVERS")}

	client, err := NewClient(&ClientConfig{
		Servers: servers,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	return client, func() {
		client.Close()
	}
}

func setOpaqueFromKey(cmds ...*protocol.Command) {
	for _, cmd := range cmds {
		cmd.Opaque = cmd.Key
	}
}
