package memcache

import (
	"bufio"
	"strings"
	"testing"
)

func BenchmarkFormatGetCommand(b *testing.B) {
	key := "test_key_123"
	flags := []string{"v", "f", "t"}
	opaque := "12345"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatGetCommand(key, flags, opaque)
	}
}

func BenchmarkFormatGetCommandNoOpaque(b *testing.B) {
	key := "test_key_123"
	flags := []string{"v"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatGetCommand(key, flags, "")
	}
}

func BenchmarkFormatSetCommand(b *testing.B) {
	key := "test_key_123"
	value := []byte("this is a test value that is reasonably long")
	ttl := 300
	flags := map[string]string{"F": "123"}
	opaque := "67890"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatSetCommand(key, value, ttl, flags, opaque)
	}
}

func BenchmarkFormatSetCommandLargeValue(b *testing.B) {
	key := "large_key"
	value := make([]byte, 1024) // 1KB value
	for i := range value {
		value[i] = byte(i % 256)
	}
	ttl := 600
	var flags map[string]string
	opaque := ""

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatSetCommand(key, value, ttl, flags, opaque)
	}
}

func BenchmarkFormatDeleteCommand(b *testing.B) {
	key := "delete_key_123"
	opaque := "999"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		formatDeleteCommand(key, opaque)
	}
}

func BenchmarkParseResponse(b *testing.B) {
	input := "VA 5 f30 c456 O789\r\nhello\r\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := bufio.NewReader(strings.NewReader(input))
		ParseResponse(reader)
	}
}

func BenchmarkParseResponseLargeValue(b *testing.B) {
	value := strings.Repeat("x", 1024) // 1KB value
	input := "VA 1024 s1024\r\n" + value + "\r\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := bufio.NewReader(strings.NewReader(input))
		ParseResponse(reader)
	}
}

func BenchmarkParseResponseSimple(b *testing.B) {
	input := "HD\r\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := bufio.NewReader(strings.NewReader(input))
		ParseResponse(reader)
	}
}

func BenchmarkIsValidKey(b *testing.B) {
	keys := []string{
		"short",
		"medium_length_key_with_underscores",
		"very_long_key_that_is_still_valid_according_to_memcache_protocol_specifications_but_approaches_the_limit",
		strings.Repeat("a", 250), // Maximum valid length
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%len(keys)]
		isValidKey(key)
	}
}

func BenchmarkIsValidKeyShort(b *testing.B) {
	key := "foo"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isValidKey(key)
	}
}

func BenchmarkIsValidKeyLong(b *testing.B) {
	key := strings.Repeat("a", 200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isValidKey(key)
	}
}
