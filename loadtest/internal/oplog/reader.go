package oplog

import (
	"bufio"
	"errors"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Reader decodes a zstd op-log stream produced by Writer. Used offline by the
// report tooling to reconstruct the operation history leading to an anomaly.
type Reader struct {
	zr  *zstd.Decoder
	br  *bufio.Reader
	buf [RecordSize]byte
}

// NewReader wraps a compressed op-log stream.
func NewReader(r io.Reader) (*Reader, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	return &Reader{zr: zr, br: bufio.NewReaderSize(zr.IOReadCloser(), 64*1024)}, nil
}

// Read returns the next record, or io.EOF when the stream is exhausted.
func (r *Reader) Read() (Record, error) {
	if _, err := io.ReadFull(r.br, r.buf[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Record{}, io.ErrUnexpectedEOF
		}
		return Record{}, err
	}
	return decode(r.buf[:]), nil
}

// Close releases the decoder.
func (r *Reader) Close() { r.zr.Close() }
