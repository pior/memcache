package meta

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

// Benchmark WriteRequest with small get request
func BenchmarkWriteRequest_SmallGet(b *testing.B) {
	req := NewRequest(CmdGet, "mykey", nil, Flag{Type: FlagReturnValue})
	b.ResetTimer()

	for b.Loop() {
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark WriteRequest with get request with multiple flags
func BenchmarkWriteRequest_GetWithFlags(b *testing.B) {
	req := NewRequest(CmdGet, "mykey", nil,
		Flag{Type: FlagReturnValue},
		Flag{Type: FlagReturnCAS},
		Flag{Type: FlagReturnTTL},
		Flag{Type: FlagReturnClientFlags},
		Flag{Type: FlagOpaque, Token: "token123"},
	)
	b.ResetTimer()

	for b.Loop() {
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark WriteRequest with small set (100 bytes)
func BenchmarkWriteRequest_SmallSet(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 100)
	req := NewRequest(CmdSet, "mykey", data, Flag{Type: FlagTTL, Token: "3600"})
	b.ResetTimer()

	for b.Loop() {
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark WriteRequest with large set (10KB)
func BenchmarkWriteRequest_LargeSet(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 10*1024)
	req := NewRequest(CmdSet, "mykey", data, Flag{Type: FlagTTL, Token: "3600"})
	b.ResetTimer()

	for b.Loop() {
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark WriteRequest with very large set (1MB)
func BenchmarkWriteRequest_VeryLargeSet(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1024*1024)
	req := NewRequest(CmdSet, "mykey", data, Flag{Type: FlagTTL, Token: "3600"})
	b.ResetTimer()

	for b.Loop() {
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark WriteRequest with arithmetic
func BenchmarkWriteRequest_Arithmetic(b *testing.B) {
	req := NewRequest(CmdArithmetic, "counter", nil,
		Flag{Type: FlagReturnValue},
		Flag{Type: FlagDelta, Token: "5"},
	)
	b.ResetTimer()

	for b.Loop() {
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark pipelining multiple requests
func BenchmarkWriteRequest_Pipeline(b *testing.B) {
	reqs := []*Request{
		NewRequest(CmdGet, "key1", nil, Flag{Type: FlagReturnValue}, Flag{Type: FlagQuiet}),
		NewRequest(CmdGet, "key2", nil, Flag{Type: FlagReturnValue}, Flag{Type: FlagQuiet}),
		NewRequest(CmdGet, "key3", nil, Flag{Type: FlagReturnValue}, Flag{Type: FlagQuiet}),
		NewRequest(CmdGet, "key4", nil, Flag{Type: FlagReturnValue}, Flag{Type: FlagQuiet}),
		NewRequest(CmdGet, "key5", nil, Flag{Type: FlagReturnValue}),
	}
	b.ResetTimer()

	for b.Loop() {
		for _, req := range reqs {
			_, err := WriteRequest(io.Discard, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// Benchmark ReadResponse with HD status
func BenchmarkReadResponse_HD(b *testing.B) {
	input := []byte("HD\r\n")
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with HD and flags
func BenchmarkReadResponse_HDWithFlags(b *testing.B) {
	input := []byte("HD c12345 t3600 f30\r\n")
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with small value (100 bytes)
func BenchmarkReadResponse_SmallValue(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 100)
	var buf bytes.Buffer
	buf.WriteString("VA 100\r\n")
	buf.Write(data)
	buf.WriteString("\r\n")
	input := buf.Bytes()
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with large value (10KB)
func BenchmarkReadResponse_LargeValue(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 10*1024)
	var buf bytes.Buffer
	buf.WriteString("VA 10240\r\n")
	buf.Write(data)
	buf.WriteString("\r\n")
	input := buf.Bytes()
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with very large value (1MB)
func BenchmarkReadResponse_VeryLargeValue(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1024*1024)
	var buf bytes.Buffer
	buf.WriteString("VA 1048576\r\n")
	buf.Write(data)
	buf.WriteString("\r\n")
	input := buf.Bytes()
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with value and flags
func BenchmarkReadResponse_ValueWithFlags(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString("VA 5 c12345 t3600 f30\r\n")
	buf.WriteString("hello\r\n")
	input := buf.Bytes()
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with EN (miss)
func BenchmarkReadResponse_Miss(b *testing.B) {
	input := []byte("EN\r\n")
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponseBatch
func BenchmarkReadResponseBatch(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString("VA 5\r\nhello\r\n")
	buf.WriteString("HD\r\n")
	buf.WriteString("EN\r\n")
	buf.WriteString("MN\r\n")
	input := buf.Bytes()
	b.ResetTimer()

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		_, err := ReadResponseBatch(r, 0, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark round-trip (write + read) for small get
func BenchmarkRoundTrip_SmallGet(b *testing.B) {
	req := NewRequest(CmdGet, "mykey", nil, Flag{Type: FlagReturnValue})
	respInput := []byte("VA 5\r\nhello\r\n")
	b.ResetTimer()

	for b.Loop() {
		// Write
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}

		// Read
		r := bufio.NewReader(bytes.NewReader(respInput))
		_, err = ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark round-trip for set operation
func BenchmarkRoundTrip_Set(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 100)
	req := NewRequest(CmdSet, "mykey", data, Flag{Type: FlagTTL, Token: "3600"})
	respInput := []byte("HD\r\n")
	b.ResetTimer()

	for b.Loop() {
		// Write
		_, err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}

		// Read
		r := bufio.NewReader(bytes.NewReader(respInput))
		_, err = ReadResponse(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}
