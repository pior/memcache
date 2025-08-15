package protocol

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *Response
		wantErr  bool
	}{
		{
			name:  "HD response",
			input: "HD\r\n",
			expected: &Response{
				Status: "HD",
				Flags:  Flags{},
			},
		},
		{
			name:  "VA response with value",
			input: "VA 5\r\nhello\r\n",
			expected: &Response{
				Status: "VA",
				Flags:  Flags{},
				Value:  []byte("hello"),
			},
		},
		{
			name:  "VA response with value and size flag",
			input: "VA 11 s11\r\nhello world\r\n",
			expected: &Response{
				Status: "VA",
				Flags:  Flags{{Type: "s", Value: "11"}},
				Value:  []byte("hello world"),
			},
		},
		{
			name:  "response with opaque",
			input: "HD O123\r\n",
			expected: &Response{
				Status: "HD",
				Flags:  Flags{},
				Opaque: "123",
			},
		},
		{
			name:  "response with flags",
			input: "VA f30 c456\r\n",
			expected: &Response{
				Status: "VA",
				Flags:  Flags{{Type: "f30", Value: ""}, {Type: "c456", Value: ""}},
			},
		},
		{
			name:    "empty response",
			input:   "\r\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tt.input))
			result, err := ReadResponse(reader)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseResponse() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("ParseResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if result.Status != tt.expected.Status {
				t.Errorf("ParseResponse() Status = %v, want %v", result.Status, tt.expected.Status)
			}

			if result.Opaque != tt.expected.Opaque {
				t.Errorf("ParseResponse() Opaque = %v, want %v", result.Opaque, tt.expected.Opaque)
			}

			if !bytes.Equal(result.Value, tt.expected.Value) {
				t.Errorf("ParseResponse() Value = %v, want %v", result.Value, tt.expected.Value)
			}

			if len(result.Flags) != len(tt.expected.Flags) {
				t.Errorf("ParseResponse() Flags length = %v, want %v", len(result.Flags), len(tt.expected.Flags))
			}

			// Check that all expected flags are present with correct values
			for _, expectedFlag := range tt.expected.Flags {
				if resultValue, exists := result.Flags.Get(expectedFlag.Type); !exists {
					t.Errorf("ParseResponse() missing flag %s", expectedFlag.Type)
				} else if resultValue != expectedFlag.Value {
					t.Errorf("ParseResponse() Flags[%s] = %v, want %v", expectedFlag.Type, resultValue, expectedFlag.Value)
				}
			}
		})
	}
}

func TestResponseParsing(t *testing.T) {
	// Test parsing of simple HD response
	response1 := "HD Ob432aa59\r\n"
	reader1 := bufio.NewReader(strings.NewReader(response1))

	resp1, err := ReadResponse(reader1)
	if err != nil {
		t.Fatalf("readResponse() error = %v", err)
	}

	if resp1.Status != "HD" {
		t.Errorf("Expected status HD, got %s", resp1.Status)
	}

	if resp1.Opaque != "b432aa59" {
		t.Errorf("Expected opaque b432aa59, got %s", resp1.Opaque)
	}

	// Test parsing of two responses in sequence
	responses := "HD Ob432aa59\r\nHD O6fcfe440\r\n"
	reader2 := bufio.NewReader(strings.NewReader(responses))

	// First response
	resp2a, err := ReadResponse(reader2)
	if err != nil {
		t.Fatalf("readResponse() first error = %v", err)
	}

	if resp2a.Status != "HD" {
		t.Errorf("First response: Expected status HD, got %s", resp2a.Status)
	}

	if resp2a.Opaque != "b432aa59" {
		t.Errorf("First response: Expected opaque b432aa59, got %s", resp2a.Opaque)
	}

	// Second response
	resp2b, err := ReadResponse(reader2)
	if err != nil {
		t.Fatalf("readResponse() second error = %v", err)
	}

	if resp2b.Status != "HD" {
		t.Errorf("Second response: Expected status HD, got %s", resp2b.Status)
	}

	if resp2b.Opaque != "6fcfe440" {
		t.Errorf("Second response: Expected opaque 6fcfe440, got %s", resp2b.Opaque)
	}
}
