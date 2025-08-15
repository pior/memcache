package protocol

import (
	"errors"
	"testing"
	"time"
)

func TestCommandToProtocol(t *testing.T) {
	tests := []struct {
		name string
		cmd  *Command
		want string
	}{
		{
			name: "get command",
			cmd:  newGetCommand("testkey"),
			want: "mg testkey v\r\n",
		},
		{
			name: "set command",
			cmd:  newSetCommand("testkey", []byte("value"), 0),
			want: "ms testkey 5\r\nvalue\r\n",
		},
		{
			name: "set command with TTL",
			cmd:  newSetCommand("testkey", []byte("value"), 60*time.Second),
			want: "ms testkey 5 T60\r\nvalue\r\n",
		},
		{
			name: "delete command",
			cmd:  newDeleteCommand("testkey"),
			want: "md testkey\r\n",
		},
		{
			name: "Meta Arithmetic",
			cmd:  newIncrementCommand("counter", 5),
			want: "ma counter D5 MI\r\n",
		},
		{
			name: "Meta Debug",
			cmd:  newDebugCommand("debug-key"),
			want: "me debug-key\r\n",
		},
		{
			name: "Meta NoOp",
			cmd:  newNoOpCommand(),
			want: "mn\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CommandToProtocol(tt.cmd)
			if result == nil {
				t.Error("commandToProtocol returned nil")
				return
			}
			assertEqualString(t, tt.want, string(result))
		})
	}
}

func TestCommandToProtocolUnsupported(t *testing.T) {
	cmd := &Command{
		Type: "unsupported",
		Key:  "test",
	}

	result := CommandToProtocol(cmd)
	if result != nil {
		t.Error("unsupported command should return nil")
	}
}

func TestProtocolToResponse(t *testing.T) {
	tests := []struct {
		name           string
		metaResp       *MetaResponse
		originalKey    string
		expectedError  error
		expectedStatus string
	}{
		{
			name: "successful response",
			metaResp: &MetaResponse{
				Status: "HD",
				Value:  []byte("test_value"),
				Flags:  Flags{{Type: "s", Value: "10"}},
				Opaque: "1234",
			},
			originalKey:    "test_key",
			expectedStatus: "HD",
		},
		{
			name: "cache miss response",
			metaResp: &MetaResponse{
				Status: "EN",
				Opaque: "1234",
			},
			originalKey:    "missing_key",
			expectedError:  ErrCacheMiss,
			expectedStatus: "EN",
		},
		{
			name: "unknown status response",
			metaResp: &MetaResponse{
				Status: "XX",
				Opaque: "1234",
			},
			originalKey:    "test_key",
			expectedError:  ErrInvalidResponse,
			expectedStatus: "XX",
		},
		{
			name: "Success MN response",
			metaResp: &MetaResponse{
				Status: StatusMN,
				Flags:  Flags{},
			},
			originalKey: "test-key",
		},
		{
			name: "Success ME response",
			metaResp: &MetaResponse{
				Status: StatusME,
				Flags:  Flags{},
			},
			originalKey: "debug-key",
		},
		{
			name: "Not found NF response",
			metaResp: &MetaResponse{
				Status: StatusNF,
				Flags:  Flags{},
			},
			originalKey:   "missing-key",
			expectedError: ErrCacheMiss,
		},
		{
			name: "Exists EX response",
			metaResp: &MetaResponse{
				Status: StatusEX,
				Flags:  Flags{},
			},
			originalKey:   "existing-key",
			expectedError: ErrCacheMiss,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := ProtocolToResponse(tt.metaResp, tt.originalKey)

			if resp.Key != tt.originalKey {
				t.Errorf("Expected key %s, got %s", tt.originalKey, resp.Key)
			}

			if resp.Status != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, resp.Status)
			}

			if tt.expectedError != nil && resp.Error == nil {
				t.Error("Expected error but got none")
			}

			if !errors.Is(resp.Error, tt.expectedError) {
				t.Errorf("Unexpected error: %v", resp.Error)
			}

			if tt.metaResp.Status == "HD" && string(resp.Value) != string(tt.metaResp.Value) {
				t.Errorf("Expected value %s, got %s", tt.metaResp.Value, resp.Value)
			}
		})
	}
}

func TestProtocolToResponseExtended(t *testing.T) {
	tests := []struct {
		name        string
		metaResp    *MetaResponse
		originalKey string
		expectError bool
		errorType   error
	}{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := ProtocolToResponse(tt.metaResp, tt.originalKey)

			if resp.Key != tt.originalKey {
				t.Errorf("expected key %s, got %s", tt.originalKey, resp.Key)
			}

			if tt.expectError && resp.Error == nil {
				t.Error("expected error but got none")
			}

			if !tt.expectError && resp.Error != nil {
				t.Errorf("expected no error but got: %v", resp.Error)
			}

			if tt.errorType != nil && resp.Error != tt.errorType {
				t.Errorf("expected error type %v, got %v", tt.errorType, resp.Error)
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Tests for constants and new protocol features
func TestConstants(t *testing.T) {
	// Test that constants are defined correctly
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Meta Get", CmdMetaGet, "mg"},
		{"Meta Set", CmdMetaSet, "ms"},
		{"Meta Delete", CmdMetaDelete, "md"},
		{"Meta Arithmetic", CmdMetaArithmetic, "ma"},
		{"Meta Debug", CmdMetaDebug, "me"},
		{"Meta NoOp", CmdMetaNoOp, "mn"},
		{"Status HD", StatusHD, "HD"},
		{"Status VA", StatusVA, "VA"},
		{"Status EN", StatusEN, "EN"},
		{"Status NS", StatusNS, "NS"},
		{"Flag Value", FlagValue, "v"},
		{"Flag CAS", FlagCAS, "c"},
		{"Flag TTL", FlagTTL, "t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.constant)
			}
		})
	}
}
