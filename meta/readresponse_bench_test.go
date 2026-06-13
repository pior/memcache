package meta

import (
	"bufio"
	"bytes"
	"testing"
)

// loopReader serves the same payload endlessly, never returning EOF.
// This models a long-lived connection wrapped in a single bufio.Reader,
// which is how ReadResponse is actually used: the bufio.Reader and its
// buffer are allocated once per connection, not once per response.
type loopReader struct {
	data []byte
	pos  int
}

func (lr *loopReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if lr.pos >= len(lr.data) {
			lr.pos = 0
		}
		c := copy(p[n:], lr.data[lr.pos:])
		lr.pos += c
		n += c
	}
	return n, nil
}

func benchReadResponse(b *testing.B, input []byte) {
	b.Helper()
	r := bufio.NewReader(&loopReader{data: input})
	var resp Response
	b.ReportAllocs()
	for b.Loop() {
		if err := ReadResponse(r, &resp); err != nil {
			b.Fatal(err)
		}
	}
}

func makeVA(size int, flags string) []byte {
	var buf bytes.Buffer
	buf.WriteString("VA ")
	buf.WriteString(itoa(size))
	if flags != "" {
		buf.WriteByte(' ')
		buf.WriteString(flags)
	}
	buf.WriteString("\r\n")
	buf.Write(bytes.Repeat([]byte("x"), size))
	buf.WriteString("\r\n")
	return buf.Bytes()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func BenchmarkReadResponseReuse_HD(b *testing.B) {
	benchReadResponse(b, []byte("HD\r\n"))
}

func BenchmarkReadResponseReuse_Miss(b *testing.B) {
	benchReadResponse(b, []byte("EN\r\n"))
}

func BenchmarkReadResponseReuse_HDWithFlags(b *testing.B) {
	benchReadResponse(b, []byte("HD c12345 t3600 f30\r\n"))
}

func BenchmarkReadResponseReuse_SmallValue(b *testing.B) {
	benchReadResponse(b, makeVA(100, ""))
}

func BenchmarkReadResponseReuse_SmallValueWithFlags(b *testing.B) {
	benchReadResponse(b, makeVA(100, "c12345 t3600 f30"))
}

func BenchmarkReadResponseReuse_LargeValue(b *testing.B) {
	benchReadResponse(b, makeVA(10*1024, ""))
}
