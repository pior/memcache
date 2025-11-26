package memcache

import (
	"hash/crc32"
	"net/netip"
	"sync"
)

type ServerSelector func(key string, servers []netip.Addr) netip.Addr

func ServerSelectorDefault(key string, servers []netip.Addr) netip.Addr {
	if len(servers) == 1 {
		return servers[0]
	}

	buf := bufferPoolForKey.Get().(*[]byte)

	n := copy(*buf, key)
	cs := crc32.ChecksumIEEE((*buf)[:n])

	bufferPoolForKey.Put(buf)

	index := cs % uint32(len(servers))
	return servers[index]
}

var bufferPoolForKey = sync.Pool{
	New: func() any {
		b := make([]byte, 256)
		return &b
	},
}
