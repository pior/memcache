// Package oplog implements the opt-in full per-operation log: every operation
// is written as a compact fixed-size binary record, streamed through zstd, for
// after-the-fact forensic analysis of crashes and desyncs. Records store a
// numeric key-id rather than the key string to stay small (~20 bytes/op raw,
// far less compressed).
package oplog

import "encoding/binary"

// RecordSize is the on-wire size of one encoded record.
const RecordSize = 20

// Record is one logged operation.
type Record struct {
	TimeNanos     int64  // nanoseconds since the run start
	Worker        uint16 // worker id
	Op            uint8  // workload.Op
	Status        uint8  // metrics.Outcome
	KeyID         uint32 // workload key id (representative key for batches)
	LatencyMicros uint32 // operation latency in microseconds
}

// encode writes the record into a 20-byte little-endian layout.
func (r Record) encode(b []byte) {
	_ = b[RecordSize-1] // bounds-check hint
	binary.LittleEndian.PutUint64(b[0:], uint64(r.TimeNanos))
	binary.LittleEndian.PutUint16(b[8:], r.Worker)
	b[10] = r.Op
	b[11] = r.Status
	binary.LittleEndian.PutUint32(b[12:], r.KeyID)
	binary.LittleEndian.PutUint32(b[16:], r.LatencyMicros)
}

// decode reads a record from a 20-byte buffer.
func decode(b []byte) Record {
	_ = b[RecordSize-1]
	return Record{
		TimeNanos:     int64(binary.LittleEndian.Uint64(b[0:])),
		Worker:        binary.LittleEndian.Uint16(b[8:]),
		Op:            b[10],
		Status:        b[11],
		KeyID:         binary.LittleEndian.Uint32(b[12:]),
		LatencyMicros: binary.LittleEndian.Uint32(b[16:]),
	}
}
