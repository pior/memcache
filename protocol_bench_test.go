package memcache

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
				readResponse(reader)
			}
		})
	}
}

func BenchmarkIsValidKey(b *testing.B) {
	tests := []struct {
		name string
		key  string
	}{
		{
			name: "Short",
			key:  "foo",
		},
		{
			name: "Medium",
			key:  "medium_length_key_with_underscores_and_numbers_123",
		},
		{
			name: "Long",
			key:  strings.Repeat("a", 200),
		},
		{
			name: "MaxLength",
			key:  strings.Repeat("a", 250),
		},
		{
			name: "Invalid",
			key:  "key with spaces",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				isValidKey(tt.key)
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
			cmd:  NewGetCommand("test_key"),
		},
		{
			name: "Set_SmallValue",
			cmd:  NewSetCommand("test_key", []byte("small_value"), 300),
		},
		{
			name: "Set_LargeValue",
			cmd:  NewSetCommand("test_key", largeValue, 300),
		},
		{
			name: "Delete",
			cmd:  NewDeleteCommand("test_key"),
		},
		{
			name: "Increment",
			cmd:  NewIncrementCommand("counter_key", 1),
		},
		{
			name: "Decrement",
			cmd:  NewDecrementCommand("counter_key", 1),
		},
		{
			name: "Debug",
			cmd:  NewDebugCommand("debug_key"),
		},
		{
			name: "NoOp",
			cmd:  NewNoOpCommand(),
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				commandToProtocol(tt.cmd)
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
		metaResp *metaResponse
		key      string
	}{
		{
			name: "Hit",
			metaResp: &metaResponse{
				Status: "VA",
				Flags:  map[string]string{"s": "5"},
				Value:  []byte("hello"),
			},
			key: "test_key",
		},
		{
			name: "Miss",
			metaResp: &metaResponse{
				Status: "EN",
				Flags:  map[string]string{},
			},
			key: "test_key",
		},
		{
			name: "LargeValue",
			metaResp: &metaResponse{
				Status: "VA",
				Flags:  map[string]string{"s": "51200"},
				Value:  largeValue,
			},
			key: "test_key",
		},
		{
			name: "Error",
			metaResp: &metaResponse{
				Status: "EX",
				Flags:  map[string]string{},
			},
			key: "test_key",
		},
		{
			name: "ManyFlags",
			metaResp: &metaResponse{
				Status: "VA",
				Flags: map[string]string{
					"s": "5",
					"f": "30",
					"c": "456",
					"t": "789",
				},
				Value: []byte("hello"),
			},
			key: "test_key",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				protocolToResponse(tt.metaResp, tt.key)
			}
		})
	}
}

func BenchmarkGenerateOpaque(b *testing.B) {
	for b.Loop() {
		generateOpaque()
	}
}

func BenchmarkProtocolWorkflows(b *testing.B) {
	// Pre-create values for benchmarks
	smallValue := []byte("test_value")
	largeValue := make([]byte, 10*1024) // 10KB
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	tests := []struct {
		name string
		fn   func()
	}{
		{
			name: "GetWorkflow_Small",
			fn: func() {
				// Format command
				cmd := NewGetCommand("workflow_key")
				_ = commandToProtocol(cmd)

				// Simulate response
				input := "VA 5 s5\r\nhello\r\n"
				reader := bufio.NewReader(strings.NewReader(input))
				metaResp, _ := readResponse(reader)

				// Convert to response
				protocolToResponse(metaResp, "workflow_key")
			},
		},
		{
			name: "SetWorkflow_Large",
			fn: func() {
				// Format command
				cmd := NewSetCommand("workflow_large_key", largeValue, 300)
				_ = commandToProtocol(cmd)

				// Simulate response
				input := "HD\r\n"
				reader := bufio.NewReader(strings.NewReader(input))
				metaResp, _ := readResponse(reader)

				// Convert to response
				protocolToResponse(metaResp, "workflow_large_key")
			},
		},
		{
			name: "MultipleOperations",
			fn: func() {
				keys := []string{"key1", "key2", "key3"}
				for _, key := range keys {
					// Set
					setCmd := NewSetCommand(key, smallValue, 300)
					_ = commandToProtocol(setCmd)

					// Get
					getCmd := NewGetCommand(key)
					_ = commandToProtocol(getCmd)

					// Delete
					delCmd := NewDeleteCommand(key)
					_ = commandToProtocol(delCmd)
				}
			},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				tt.fn()
			}
		})
	}
}
