package oplog

import (
	"bufio"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Writer streams records through zstd. Safe for concurrent use: the per-op hot
// path takes a short mutex (the full op-log is opt-in and used for stress runs,
// where forensic completeness outweighs the lock cost). For maximum throughput
// give each worker its own Writer over a separate shard file.
type Writer struct {
	mu  sync.Mutex
	bw  *bufio.Writer
	zw  *zstd.Encoder
	buf [RecordSize]byte
}

// NewWriter wraps w with buffering and zstd compression.
func NewWriter(w io.Writer) (*Writer, error) {
	bw := bufio.NewWriterSize(w, 64*1024)
	zw, err := zstd.NewWriter(bw, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	return &Writer{bw: bw, zw: zw}, nil
}

// Write appends one record.
func (w *Writer) Write(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	r.encode(w.buf[:])
	_, err := w.zw.Write(w.buf[:])
	return err
}

// Close flushes the zstd stream and the underlying buffer.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.zw.Close(); err != nil {
		return err
	}
	return w.bw.Flush()
}
