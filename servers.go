package memcache

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
)

type Servers interface {
	List() []netip.Addr
}

func ServersList(servers ...string) (Servers, error) {
	return newStaticServers(servers)
}

func MustServersList(servers ...string) Servers {
	s, err := newStaticServers(servers)
	if err != nil {
		panic(err)
	}
	return s
}

func ServersFromEnv(envVar string) (Servers, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, errors.New("missing environment variable " + envVar)
	}

	entries := strings.Split(value, ",")
	return newStaticServers(entries)
}

func newStaticServers(entries []string) (*staticServers, error) {
	servers := make([]netip.Addr, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, err
		}

		servers = append(servers, addr)
	}

	return &staticServers{servers: servers}, nil
}

type staticServers struct {
	mu      sync.RWMutex
	servers []netip.Addr
}

func (s *staticServers) updateServers(addrs []net.Addr) {
	servers := make([]netip.Addr, len(addrs))

	for i, addr := range addrs {
		ip, err := net.ResolveIPAddr(addr.Network(), addr.String())
		if err != nil {
			continue
		}

		parsedIP, err := netip.ParseAddr(ip.String())
		if err != nil {
			continue
		}
		servers[i] = parsedIP
	}

	s.mu.Lock()
	s.servers = servers
	s.mu.Unlock()
}

func (s *staticServers) List() []netip.Addr {
	s.mu.RLock()
	server := s.servers
	s.mu.RUnlock()
	return server
}
