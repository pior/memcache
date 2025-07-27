package memcache

import (
	"testing"
)

func TestCommandToProtocol(t *testing.T) {
	tests := []struct {
		name string
		cmd  *Command
		want string
	}{
		{
			name: "get command",
			cmd:  NewGetCommand("testkey"),
			want: "mg testkey v O",
		},
		{
			name: "set command",
			cmd:  NewSetCommand("testkey", []byte("value"), 0),
			want: "ms testkey 5",
		},
		{
			name: "delete command",
			cmd:  NewDeleteCommand("testkey"),
			want: "md testkey O",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := commandToProtocol(tt.cmd)
			if result == nil {
				t.Error("commandToProtocol returned nil")
				return
			}

			resultStr := string(result)
			// Just check that the command starts correctly since opaque is random
			if tt.cmd.Type == "mg" && !contains(resultStr, "mg testkey v") {
				t.Errorf("Get command should contain 'mg testkey v', got: %s", resultStr)
			}
			if tt.cmd.Type == "ms" && !contains(resultStr, "ms testkey 5") {
				t.Errorf("Set command should contain 'ms testkey 5', got: %s", resultStr)
			}
			if tt.cmd.Type == "md" && !contains(resultStr, "md testkey") {
				t.Errorf("Delete command should contain 'md testkey', got: %s", resultStr)
			}
		})
	}
}

func TestCommandToProtocolUnsupported(t *testing.T) {
	cmd := &Command{
		Type: "unsupported",
		Key:  "test",
	}

	result := commandToProtocol(cmd)
	if result != nil {
		t.Error("unsupported command should return nil")
	}
}

func TestProtocolToResponse(t *testing.T) {
	tests := []struct {
		name           string
		metaResp       *MetaResponse
		originalKey    string
		expectError    bool
		expectedStatus string
	}{
		{
			name: "successful response",
			metaResp: &MetaResponse{
				Status: "HD",
				Value:  []byte("test_value"),
				Flags:  map[string]string{"s": "10"},
				Opaque: "1234",
			},
			originalKey:    "test_key",
			expectError:    false,
			expectedStatus: "HD",
		},
		{
			name: "cache miss response",
			metaResp: &MetaResponse{
				Status: "EN",
				Opaque: "1234",
			},
			originalKey:    "missing_key",
			expectError:    true,
			expectedStatus: "EN",
		},
		{
			name: "unknown status response",
			metaResp: &MetaResponse{
				Status: "XX",
				Opaque: "1234",
			},
			originalKey:    "test_key",
			expectError:    true,
			expectedStatus: "XX",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := protocolToResponse(tt.metaResp, tt.originalKey)

			if resp.Key != tt.originalKey {
				t.Errorf("Expected key %s, got %s", tt.originalKey, resp.Key)
			}

			if resp.Status != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, resp.Status)
			}

			if resp.Opaque != tt.metaResp.Opaque {
				t.Errorf("Expected opaque %s, got %s", tt.metaResp.Opaque, resp.Opaque)
			}

			if tt.expectError && resp.Error == nil {
				t.Error("Expected error but got none")
			}

			if !tt.expectError && resp.Error != nil {
				t.Errorf("Unexpected error: %v", resp.Error)
			}

			if tt.metaResp.Status == "HD" && string(resp.Value) != string(tt.metaResp.Value) {
				t.Errorf("Expected value %s, got %s", tt.metaResp.Value, resp.Value)
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
