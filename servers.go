package memcache

import (
	"fmt"
	"os"
	"strings"
)

// Server identifies a memcache node.
//
// Address is the host:port used to dial the node and the key under which its
// connection pool is tracked. It is the stable identity a ServerSelector hashes
// against, so selection is consistent across processes and survives reordering
// of the server set. More fields (e.g. weight, zone) may be added in a
// backward-compatible way; the meaning of Address must not change.
type Server struct {
	Address string
}

// Servers provides the current set of memcache servers.
// Implementations must be safe for concurrent use.
type Servers interface {
	// List returns the current set of servers.
	List() []Server
}

type servers []Server

// StaticServers returns an immutable Servers built from the given host:port
// addresses, in the order provided.
func StaticServers(addrs ...string) servers {
	s := make(servers, len(addrs))
	for i, addr := range addrs {
		s[i] = Server{Address: addr}
	}
	return s
}

func (s servers) List() []Server {
	return []Server(s)
}

// ServersFromEnv creates a Servers instance from a comma-separated list of
// server addresses stored in the specified environment variable.
func ServersFromEnv(envVar string) (Servers, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("environment variable %s not set", envVar)
	}

	var addrs []string
	for addr := range strings.SplitSeq(value, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("environment variable %s contains no server address", envVar)
	}
	return StaticServers(addrs...), nil
}
