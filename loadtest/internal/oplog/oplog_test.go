package oplog

import (
	"bytes"
	"io"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	r := Record{TimeNanos: 123456789, Worker: 7, Op: 3, Status: 1, KeyID: 4242, LatencyMicros: 850}
	var b [RecordSize]byte
	r.encode(b[:])
	got := decode(b[:])
	if got != r {
		t.Errorf("round trip = %+v, want %+v", got, r)
	}
}

func TestWriterReaderStream(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]Record, 1000)
	for i := range want {
		want[i] = Record{TimeNanos: int64(i) * 1000, Worker: uint16(i % 8), Op: uint8(i % 6), Status: uint8(i % 3), KeyID: uint32(i), LatencyMicros: uint32(i)}
		if err := w.Write(want[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// zstd should compress the repetitive stream well below the raw size.
	if buf.Len() >= len(want)*RecordSize {
		t.Errorf("compressed size %d not below raw %d", buf.Len(), len(want)*RecordSize)
	}

	r, err := NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := range want {
		got, err := r.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got != want[i] {
			t.Fatalf("record %d = %+v, want %+v", i, got, want[i])
		}
	}
	if _, err := r.Read(); err != io.EOF {
		t.Errorf("expected EOF after last record, got %v", err)
	}
}
