package memcache

import (
	"fmt"
	"os"
	"strings"
)

// Servers provides the list of memcache server addresses.
// Implementations must be safe for concurrent use.
type Servers interface {
	// List returns the current list of server addresses.
	List() []string
}

type servers []string

// StaticServers returns a Servers with the given server addresses.
func StaticServers(addrs ...string) servers {
	return servers(addrs)
}

func (s servers) List() []string {
	return []string(s)
}

// ServersFromEnv creates a Servers instance from a comma-separated list of
// server addresses stored in the specified environment variable.
func ServersFromEnv(envVar string) (Servers, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("environment variable %s not set", envVar)
	}

	addrs := strings.Split(value, ",")
	return StaticServers(addrs...), nil
}
