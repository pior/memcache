package memcache

import (
	"bytes"
	"sync"
)

type byteBufferPool struct {
	pool sync.Pool
}

func newByteBufferPool(initialSize int) *byteBufferPool {
	return &byteBufferPool{
		pool: sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, initialSize))
			},
		},
	}
}

func (p *byteBufferPool) Get() *bytes.Buffer {
	return p.pool.Get().(*bytes.Buffer)
}

func (b *byteBufferPool) Put(buf *bytes.Buffer) {
	buf.Reset()
	b.pool.Put(buf)
}
