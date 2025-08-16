package memcache

import (
	"errors"
	"hash/crc32"
	"log/slog"
	"sync"
)

var (
	ErrNoServersAvailable = errors.New("memcache: no servers available")
)

type Selector func(serverAddresses []string, key string) (serverAddress string)

func DefaultSelector(serverAddresses []string, key string) string {
	if len(serverAddresses) == 0 {
		slog.Error("memcache: cannot select server: server list is empty")
		return ""
	}

	if len(serverAddresses) == 1 {
		return serverAddresses[0]
	}

	bufp := keyBufPool.Get().(*[]byte)
	n := copy(*bufp, key)

	cs := crc32.ChecksumIEEE((*bufp)[:n])
	keyBufPool.Put(bufp)

	return serverAddresses[cs%uint32(len(serverAddresses))]
}

// keyBufPool returns []byte buffers for use by DefaultSelector's call to crc32.ChecksumIEEE to avoid allocations.
// (but doesn't avoid the copies, which at least are bounded in size and small)
var keyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 256)
		return &b
	},
}
