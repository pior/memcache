package protocol

import (
	"bufio"
	"strings"
	"testing"
)

func BenchmarkReadResponse(b *testing.B) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "SimpleStatus",
			input: "HD\r\n",
		},
		{
			name:  "StatusWithFlags",
			input: "HD f30 c456 O789\r\n",
		},
		{
			name:  "SmallValue",
			input: "VA 5 s5\r\nhello\r\n",
		},
		{
			name:  "MediumValue",
			input: "VA 1024 s1024\r\n" + strings.Repeat("x", 1024) + "\r\n",
		},
		{
			name:  "LargeValue",
			input: "VA 102400 s102400\r\n" + strings.Repeat("x", 100*1024) + "\r\n",
		},
		{
			name:  "ValueWithManyFlags",
			input: "VA 5 f30 c456 t789 s5 O123\r\nhello\r\n",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				reader := bufio.NewReader(strings.NewReader(tt.input))
				ReadResponse(reader)
			}
		})
	}
}

func BenchmarkCommandToProtocol(b *testing.B) {
	// Pre-create large value for benchmarks
	largeValue := make([]byte, 100*1024) // 100KB
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	tests := []struct {
		name string
		cmd  *Command
	}{
		{
			name: "Get",
			cmd:  newGetCommand("test_key"),
		},
		{
			name: "Set_SmallValue",
			cmd:  newSetCommand("test_key", []byte("small_value"), 300),
		},
		{
			name: "Set_LargeValue",
			cmd:  newSetCommand("test_key", largeValue, 300),
		},
		{
			name: "Delete",
			cmd:  newDeleteCommand("test_key"),
		},
		{
			name: "Increment",
			cmd:  newIncrementCommand("counter_key", 1),
		},
		{
			name: "Decrement",
			cmd:  newDecrementCommand("counter_key", 1),
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				CommandToProtocol(tt.cmd)
			}
		})
	}
}

func BenchmarkProtocolToResponse(b *testing.B) {
	// Pre-create large value for benchmarks
	largeValue := make([]byte, 50*1024) // 50KB
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	tests := []struct {
		name     string
		metaResp *MetaResponse
		key      string
	}{
		{
			name: "Hit",
			metaResp: &MetaResponse{
				Status: "VA",
				Flags:  Flags{{Type: "s", Value: "5"}},
				Value:  []byte("hello"),
			},
			key: "test_key",
		},
		{
			name: "Miss",
			metaResp: &MetaResponse{
				Status: "EN",
				Flags:  Flags{},
			},
			key: "test_key",
		},
		{
			name: "LargeValue",
			metaResp: &MetaResponse{
				Status: "VA",
				Flags:  Flags{{Type: "s", Value: "51200"}},
				Value:  largeValue,
			},
			key: "test_key",
		},
		{
			name: "Error",
			metaResp: &MetaResponse{
				Status: "EX",
				Flags:  Flags{},
			},
			key: "test_key",
		},
		{
			name: "ManyFlags",
			metaResp: &MetaResponse{
				Status: "VA",
				Flags: Flags{
					{Type: "s", Value: "5"},
					{Type: "f", Value: "30"},
					{Type: "c", Value: "456"},
					{Type: "t", Value: "789"},
				},
				Value: []byte("hello"),
			},
			key: "test_key",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				ProtocolToResponse(tt.metaResp, tt.key)
			}
		})
	}
}

func BenchmarkGenerateOpaque(b *testing.B) {
	cmd := NewCommand("", "")

	for b.Loop() {
		SetRandomOpaque(cmd)
	}
}
