package meta

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

var sinkRequest *Request

// goos: darwin
// goarch: arm64
// pkg: github.com/pior/memcache/meta
// cpu: Apple M2
// BenchmarkWriteRequest/SmallGet/discard-8 				25417876	        47.07 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteRequest/GetWithFlags/discard-8         	16368240	        72.80 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteRequest/SmallSet/discard-8             	15752071	        74.13 ns/op	       3 B/op	       1 allocs/op
// BenchmarkWriteRequest/LargeSet/discard-8             	15585992	        76.51 ns/op	       5 B/op	       1 allocs/op
// BenchmarkWriteRequest/VeryLargeSet/discard-8         	14929620	        79.78 ns/op	       8 B/op	       1 allocs/op
// BenchmarkWriteRequest/Arithmetic/discard-8           	18966021	        63.22 ns/op	       0 B/op	       0 allocs/op
func BenchmarkBuildRequest(b *testing.B) {
	b.Run("GetNoFlags", func(b *testing.B) {
		for b.Loop() {
			sinkRequest = NewRequest(CmdGet, "mykey", nil)
		}
	})

	b.Run("GetWithFlags", func(b *testing.B) {
		for b.Loop() {
			req := NewRequest(CmdGet, "mykey", nil)
			req.AddReturnValue()
			req.AddReturnCAS()
			req.AddReturnTTL()
			req.AddReturnClientFlags()
			req.AddOpaque("token123")
			sinkRequest = req
		}
	})

	b.Run("SetWithTTL", func(b *testing.B) {
		data := bytes.Repeat([]byte("x"), 100)
		for b.Loop() {
			req := NewRequest(CmdSet, "mykey", data)
			req.AddTTL(3600)
			sinkRequest = req
		}
	})

	b.Run("Arithmetic", func(b *testing.B) {
		for b.Loop() {
			req := NewRequest(CmdArithmetic, "counter", nil)
			req.AddReturnValue()
			req.AddDelta(5)
			sinkRequest = req
		}
	})
}

func BenchmarkWriteRequest(b *testing.B) {
	b.Run("SmallGet", func(b *testing.B) {
		req := NewRequest(CmdGet, "mykey", nil)
		req.AddReturnValue()
		runWriteRequestBenchmarks(b, req)
	})

	b.Run("GetWithFlags", func(b *testing.B) {
		req := NewRequest(CmdGet, "mykey", nil)
		req.AddReturnValue()
		req.AddReturnCAS()
		req.AddReturnTTL()
		req.AddReturnClientFlags()
		req.AddOpaque("token123")
		runWriteRequestBenchmarks(b, req)
	})

	b.Run("SmallSet", func(b *testing.B) {
		data := bytes.Repeat([]byte("x"), 100)
		req := NewRequest(CmdSet, "mykey", data)
		req.AddTTL(3600)
		runWriteRequestBenchmarks(b, req)
	})

	b.Run("LargeSet", func(b *testing.B) {
		data := bytes.Repeat([]byte("x"), 10*1024)
		req := NewRequest(CmdSet, "mykey", data)
		req.AddTTL(3600)
		runWriteRequestBenchmarks(b, req)
	})

	b.Run("VeryLargeSet", func(b *testing.B) {
		data := bytes.Repeat([]byte("x"), 1024*1024)
		req := NewRequest(CmdSet, "mykey", data)
		req.AddTTL(3600)
		runWriteRequestBenchmarks(b, req)
	})

	b.Run("Arithmetic", func(b *testing.B) {
		req := NewRequest(CmdArithmetic, "counter", nil)
		req.AddReturnValue()
		req.AddDelta(5)
		runWriteRequestBenchmarks(b, req)
	})
}

func runWriteRequestBenchmarks(b *testing.B, req *Request) {
	b.Helper()

	b.Run("discard", func(b *testing.B) {
		for b.Loop() {
			err := WriteRequest(io.Discard, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// b.Run("connection", func(b *testing.B) {
	// 	conn := openTCPConnectionForWriting(b)
	// 	writer := bufio.NewWriter(conn)

	// 	for b.Loop() {
	// 		err := WriteRequest(writer, req)
	// 		if err != nil {
	// 			b.Fatal(err)
	// 		}
	// 		err = writer.Flush()
	// 		if err != nil {
	// 			b.Fatal(err)
	// 		}
	// 	}
	// })
}

// Benchmark ReadResponse with HD status
func BenchmarkReadResponse_HD(b *testing.B) {
	input := []byte("HD\r\n")

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with HD and flags
func BenchmarkReadResponse_HDWithFlags(b *testing.B) {
	input := []byte("HD c12345 t3600 f30\r\n")

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
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

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
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

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
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

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
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

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark ReadResponse with EN (miss)
func BenchmarkReadResponse_Miss(b *testing.B) {
	input := []byte("EN\r\n")

	for b.Loop() {
		r := bufio.NewReader(bytes.NewReader(input))
		var resp Response
		err := ReadResponse(r, &resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark round-trip (write + read) for small get
func BenchmarkRoundTrip_SmallGet(b *testing.B) {
	req := NewRequest(CmdGet, "mykey", nil)
	req.AddReturnValue()
	respInput := []byte("VA 5\r\nhello\r\n")

	for b.Loop() {
		// Write
		err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}

		// Read
		r := bufio.NewReader(bytes.NewReader(respInput))
		var resp Response
		err = ReadResponse(r, &resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark round-trip for set operation
func BenchmarkRoundTrip_Set(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 100)
	req := NewRequest(CmdSet, "mykey", data)
	req.AddTTL(3600)
	respInput := []byte("HD\r\n")

	for b.Loop() {
		// Write
		err := WriteRequest(io.Discard, req)
		if err != nil {
			b.Fatal(err)
		}

		// Read
		r := bufio.NewReader(bytes.NewReader(respInput))
		var resp Response
		err = ReadResponse(r, &resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// openTCPConnectionForWriting opens a TCP connection to a local listener that discards all data.
// func openTCPConnectionForWriting(b *testing.B) net.Conn {
// 	listener, err := net.Listen("tcp", "localhost:0") // let the OS pick an available port
// 	require.NoError(b, err)
// 	defer listener.Close()

// 	go func() {
// 		for {
// 			conn, err := listener.Accept()
// 			if err != nil {
// 				return
// 			}
// 			go io.Copy(io.Discard, conn)
// 		}
// 	}()

// 	conn, err := net.Dial("tcp", listener.Addr().String())
// 	require.NoError(b, err)

// 	b.Cleanup(func() {
// 		conn.Close()
// 		listener.Close()
// 	})

// 	return conn
// }
