package memcache

import (
	"errors"
	"hash/crc32"
)

var ErrNoServers = errors.New("no servers available")

type Servers interface {
	Select(key string) string
}

type servers struct {
	addresses []string
}

func ServersFromAddr(addresses ...string) Servers {
	if len(addresses) == 0 {
		panic("ServersFromAddr requires at least one address")
	}

	return &servers{
		addresses: addresses,
	}
}

func (s *servers) Select(key string) string {
	cs := crc32.ChecksumIEEE(([]byte(key)))
	index := cs % uint32(len(s.addresses))

	return s.addresses[index]
}
