package meta

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testMemcachedAddr = "127.0.0.1:11211"
	testTimeout       = 5 * time.Second
)

func dialMemcached(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", testMemcachedAddr, testTimeout)
	if err != nil {
		t.Skipf("Skipping integration test: memcached not available at %s: %v", testMemcachedAddr, err)
	}

	if err := conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
		conn.Close()
		t.Fatalf("Failed to set deadline: %v", err)
	}

	t.Cleanup(func() {
		conn.Close()
	})

	return conn, bufio.NewReader(conn)
}

func TestIntegration_Get(t *testing.T) {
	conn, r := dialMemcached(t)

	// First, set a value with 60 second TTL
	setReq := NewRequest(CmdSet, "test_get_key", []byte("test_value"), []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
	})
	err := WriteRequest(conn, setReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	setResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}
	if !setResp.IsSuccess() {
		t.Fatalf("Set failed: status=%s", setResp.Status)
	}

	// Now get it back with value returned
	getReq := NewRequest(CmdGet, "test_get_key", nil, []Flag{
		{Type: FlagReturnValue}, // v - return the value in response
	})
	err = WriteRequest(conn, getReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	getResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !getResp.HasValue() {
		t.Fatalf("Expected value, got status=%s", getResp.Status)
	}

	if string(getResp.Data) != "test_value" {
		t.Errorf("Got value %q, want %q", string(getResp.Data), "test_value")
	}
}

func TestIntegration_GetMiss(t *testing.T) {
	conn, r := dialMemcached(t)

	// Get non-existent key
	req := NewRequest(CmdGet, "nonexistent_key_12345", nil, []Flag{
		{Type: FlagReturnValue}, // v - would return value if it existed
	})
	err := WriteRequest(conn, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !resp.IsMiss() {
		t.Errorf("Expected miss, got status=%s", resp.Status)
	}
}

func TestIntegration_GetWithFlags(t *testing.T) {
	conn, r := dialMemcached(t)

	// Set a value with client flags
	setReq := NewRequest(CmdSet, "test_flags_key", []byte("value"), []Flag{
		{Type: FlagTTL, Token: "60"},          // T60 - set TTL to 60 seconds
		{Type: FlagClientFlags, Token: "123"}, // F123 - set client flags to 123
	})
	err := WriteRequest(conn, setReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	setResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}
	if !setResp.IsSuccess() {
		t.Fatalf("Set failed: status=%s", setResp.Status)
	}

	// Get with metadata - request multiple pieces of item metadata
	getReq := NewRequest(CmdGet, "test_flags_key", nil, []Flag{
		{Type: FlagReturnValue},       // v - return value data
		{Type: FlagReturnCAS},         // c - return CAS token
		{Type: FlagReturnTTL},         // t - return remaining TTL
		{Type: FlagReturnClientFlags}, // f - return client flags
		{Type: FlagReturnSize},        // s - return value size in bytes
	})
	err = WriteRequest(conn, getReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	getResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !getResp.HasValue() {
		t.Fatalf("Expected value, got status=%s", getResp.Status)
	}

	// Check flags in response
	if !getResp.HasFlag(FlagReturnCAS) {
		t.Error("Expected CAS flag")
	}
	if !getResp.HasFlag(FlagReturnTTL) {
		t.Error("Expected TTL flag")
	}
	if !getResp.HasFlag(FlagReturnClientFlags) {
		t.Error("Expected client flags")
	}
	if getResp.GetFlagToken(FlagReturnClientFlags) != "123" {
		t.Errorf("Got client flags %q, want %q", getResp.GetFlagToken(FlagReturnClientFlags), "123")
	}
	if !getResp.HasFlag(FlagReturnSize) {
		t.Error("Expected size flag")
	}
	if getResp.GetFlagToken(FlagReturnSize) != "5" { // "value" is 5 bytes
		t.Errorf("Got size %q, want %q", getResp.GetFlagToken(FlagReturnSize), "5")
	}
}

func TestIntegration_Set(t *testing.T) {
	conn, r := dialMemcached(t)

	// Basic set with TTL
	req := NewRequest(CmdSet, "test_set_key", []byte("hello world"), []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
	})
	err := WriteRequest(conn, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !resp.IsSuccess() {
		t.Errorf("Expected success, got status=%s", resp.Status)
	}
}

func TestIntegration_SetLarge(t *testing.T) {
	conn, r := dialMemcached(t)

	// Set large value (10KB)
	data := bytes.Repeat([]byte("A"), 10*1024)

	req := NewRequest(CmdSet, "test_large_key", data, []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
	})
	err := WriteRequest(conn, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !resp.IsSuccess() {
		t.Errorf("Expected success, got status=%s", resp.Status)
	}

	// Verify we can get it back
	getReq := NewRequest(CmdGet, "test_large_key", nil, []Flag{
		{Type: FlagReturnValue}, // v - return the value
	})
	err = WriteRequest(conn, getReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	getResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !getResp.HasValue() {
		t.Fatalf("Expected value, got status=%s", getResp.Status)
	}

	if len(getResp.Data) != len(data) {
		t.Errorf("Got data length %d, want %d", len(getResp.Data), len(data))
	}
}

func TestIntegration_SetAdd(t *testing.T) {
	conn, r := dialMemcached(t)

	key := "test_add_key"

	// Delete key first to ensure it doesn't exist
	delReq := NewRequest(CmdDelete, key, nil, nil)
	if err := WriteRequest(conn, delReq); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	if _, err := ReadResponse(r); err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	// Add should succeed - ModeAdd only stores if key doesn't exist
	addReq := NewRequest(CmdSet, key, []byte("value1"), []Flag{
		{Type: FlagMode, Token: ModeAdd}, // ME - add mode (only store if not exists)
		{Type: FlagTTL, Token: "60"},     // T60 - set TTL to 60 seconds
	})
	err := WriteRequest(conn, addReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	addResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !addResp.IsSuccess() {
		t.Errorf("First add should succeed, got status=%s", addResp.Status)
	}

	// Second add should fail (NS) - key already exists
	addReq2 := NewRequest(CmdSet, key, []byte("value2"), []Flag{
		{Type: FlagMode, Token: ModeAdd}, // ME - add mode
		{Type: FlagTTL, Token: "60"},
	})
	err = WriteRequest(conn, addReq2)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	addResp2, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !addResp2.IsNotStored() {
		t.Errorf("Second add should fail with NS, got status=%s", addResp2.Status)
	}
}

func TestIntegration_Delete(t *testing.T) {
	conn, r := dialMemcached(t)

	key := "test_delete_key"

	// Set a value
	setReq := NewRequest(CmdSet, key, []byte("value"), []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
	})
	if err := WriteRequest(conn, setReq); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	if _, err := ReadResponse(r); err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	// Delete it
	delReq := NewRequest(CmdDelete, key, nil, nil)
	if err := WriteRequest(conn, delReq); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	delResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !delResp.IsSuccess() {
		t.Errorf("Delete should succeed, got status=%s", delResp.Status)
	}

	// Verify it's gone
	getReq := NewRequest(CmdGet, key, nil, []Flag{
		{Type: FlagReturnValue}, // v - return value (will be miss)
	})
	err = WriteRequest(conn, getReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	getResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !getResp.IsMiss() {
		t.Errorf("Expected miss after delete, got status=%s", getResp.Status)
	}
}

func TestIntegration_Arithmetic(t *testing.T) {
	conn, r := dialMemcached(t)

	key := "test_counter"

	// Delete first to start fresh
	delReq := NewRequest(CmdDelete, key, nil, nil)
	if err := WriteRequest(conn, delReq); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	if _, err := ReadResponse(r); err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	// Create counter with initial value
	setReq := NewRequest(CmdSet, key, []byte("100"), []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
	})
	if err := WriteRequest(conn, setReq); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	if _, err := ReadResponse(r); err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	// Increment by 5 (default mode is increment)
	incrReq := NewRequest(CmdArithmetic, key, nil, []Flag{
		{Type: FlagReturnValue},       // v - return the new value
		{Type: FlagDelta, Token: "5"}, // D5 - delta of 5
	})
	if err := WriteRequest(conn, incrReq); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	incrResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !incrResp.HasValue() {
		t.Fatalf("Expected value, got status=%s", incrResp.Status)
	}

	if string(incrResp.Data) != "105" {
		t.Errorf("Got value %q, want %q", string(incrResp.Data), "105")
	}

	// Decrement by 3
	decrReq := NewRequest(CmdArithmetic, key, nil, []Flag{
		{Type: FlagReturnValue},                // v - return the new value
		{Type: FlagMode, Token: ModeDecrement}, // MD - decrement mode
		{Type: FlagDelta, Token: "3"},          // D3 - delta of 3
	})
	err = WriteRequest(conn, decrReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	decrResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !decrResp.HasValue() {
		t.Fatalf("Expected value, got status=%s", decrResp.Status)
	}

	if string(decrResp.Data) != "102" {
		t.Errorf("Got value %q, want %q", string(decrResp.Data), "102")
	}
}

func TestIntegration_NoOp(t *testing.T) {
	conn, r := dialMemcached(t)

	req := NewRequest(CmdNoOp, "", nil, nil)
	err := WriteRequest(conn, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if resp.Status != StatusMN {
		t.Errorf("Expected MN status, got %s", resp.Status)
	}
}

func TestIntegration_Pipelining(t *testing.T) {
	conn, r := dialMemcached(t)

	// Set up test keys
	for i := 1; i <= 3; i++ {
		key := "pipe_key" + strconv.Itoa(i)
		value := "value" + strconv.Itoa(i)
		setReq := NewRequest(CmdSet, key, []byte(value), []Flag{
			{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
		})
		if err := WriteRequest(conn, setReq); err != nil {
			t.Fatalf("WriteRequest failed: %v", err)
		}
		if _, err := ReadResponse(r); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}
	}

	// Pipeline multiple gets with quiet mode
	// FlagQuiet suppresses miss responses (EN), only hits and errors are returned
	reqs := []*Request{
		NewRequest(CmdGet, "pipe_key1", nil, []Flag{
			{Type: FlagReturnValue}, // v - return value
			{Type: FlagReturnKey},   // k - return key in response
			{Type: FlagQuiet},       // q - suppress miss response
		}),
		NewRequest(CmdGet, "pipe_key2", nil, []Flag{
			{Type: FlagReturnValue},
			{Type: FlagReturnKey},
			{Type: FlagQuiet},
		}),
		NewRequest(CmdGet, "pipe_key3", nil, []Flag{
			{Type: FlagReturnValue},
			{Type: FlagReturnKey},
			{Type: FlagQuiet},
		}),
		NewRequest(CmdGet, "nonexistent", nil, []Flag{ // This won't return due to quiet mode
			{Type: FlagReturnValue},
			{Type: FlagReturnKey},
			{Type: FlagQuiet},
		}),
		NewRequest(CmdNoOp, "", nil, nil), // mn - signals end of pipeline
	}

	// Send all requests
	for _, req := range reqs {
		err := WriteRequest(conn, req)
		if err != nil {
			t.Fatalf("WriteRequest failed: %v", err)
		}
	}

	// Read responses (only hits + MN)
	resps, err := ReadResponseBatch(r, 0, true)
	if err != nil {
		t.Fatalf("ReadResponseBatch failed: %v", err)
	}

	// Should get 3 hits + MN = 4 responses
	if len(resps) != 4 {
		t.Errorf("Expected 4 responses (3 hits + MN), got %d", len(resps))
	}

	// Verify we got the values
	hitCount := 0
	for _, resp := range resps {
		if resp.Status == StatusVA {
			hitCount++
		}
	}

	if hitCount != 3 {
		t.Errorf("Expected 3 hits, got %d", hitCount)
	}
}

func TestIntegration_CAS(t *testing.T) {
	conn, r := dialMemcached(t)

	key := "test_cas_key"

	// Set initial value and request CAS token in response
	setReq := NewRequest(CmdSet, key, []byte("value1"), []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
		{Type: FlagReturnCAS},        // c - return CAS value in response
	})
	err := WriteRequest(conn, setReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	setResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	casValue := setResp.GetFlagToken(FlagReturnCAS)
	if casValue == "" {
		t.Fatal("Expected CAS value in response")
	}

	// Update with correct CAS should succeed (compare-and-swap)
	updateReq := NewRequest(CmdSet, key, []byte("value2"), []Flag{
		{Type: FlagCAS, Token: casValue}, // C<cas> - only store if CAS matches
		{Type: FlagTTL, Token: "60"},
	})
	err = WriteRequest(conn, updateReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	updateResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !updateResp.IsSuccess() {
		t.Errorf("CAS update should succeed, got status=%s", updateResp.Status)
	}

	// Update with wrong CAS should fail with EX (exists/mismatch)
	badUpdateReq := NewRequest(CmdSet, key, []byte("value3"), []Flag{
		{Type: FlagCAS, Token: "99999"}, // Wrong CAS value
		{Type: FlagTTL, Token: "60"},
	})
	err = WriteRequest(conn, badUpdateReq)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	badUpdateResp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !badUpdateResp.IsCASMismatch() {
		t.Errorf("Bad CAS should fail with EX, got status=%s", badUpdateResp.Status)
	}
}

// TestIntegration_ClientError tests that invalid keys are rejected client-side
func TestIntegration_ClientError(t *testing.T) {
	conn, _ := dialMemcached(t)

	// Attempt to send a request with invalid key length (>250 bytes)
	longKey := strings.Repeat("a", MaxKeyLength+1)

	req := NewRequest(CmdGet, longKey, nil, nil)
	err := WriteRequest(conn, req)
	if err == nil {
		t.Fatal("WriteRequest should fail for invalid key, but succeeded")
	}

	var wantErr *InvalidKeyError
	if !errors.As(err, &wantErr) {
		t.Fatalf("Expected InvalidKeyError, got %T", err)
	}

	// Verify we get a meaningful error message
	if wantErr.Error() != "key exceeds maximum length of 250 bytes" {
		t.Errorf("Expected error about maximum length, got: %v", err)
	}
}

// TestIntegration_InvalidFlags tests that invalid flag combinations trigger errors
func TestIntegration_InvalidFlags(t *testing.T) {
	conn, r := dialMemcached(t)

	// Send ms with conflicting mode flags (S and E)
	// Note: This may not trigger CLIENT_ERROR on all memcached versions,
	// but tests error handling robustness
	req := NewRequest(CmdSet, "testkey", []byte("testvalue"), []Flag{
		{Type: 'S'},
		{Type: 'E'},
	})

	err := WriteRequest(conn, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	// Read response - might be CLIENT_ERROR or a failure status
	resp, err := ReadResponse(r)
	if err != nil {
		// Got an error - verify it's a proper error type
		if !ShouldCloseConnection(err) {
			t.Logf("Got error (connection reusable): %v", err)
		} else {
			t.Logf("Got error (must close connection): %v", err)
		}
	} else {
		// Got a response - should indicate failure
		if resp.IsSuccess() {
			t.Errorf("Conflicting flags should not succeed")
		}
		t.Logf("Got failure response: %s", resp.Status)
	}
}

// TestIntegration_ProtocolErrors tests handling of malformed protocol responses
func TestIntegration_ProtocolErrors(t *testing.T) {
	conn, r := dialMemcached(t)

	// Send completely invalid command by writing directly
	_, err := conn.Write([]byte("INVALID COMMAND\r\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should get ERROR or CLIENT_ERROR
	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !resp.HasError() {
		t.Fatalf("Expected error response for invalid command, got: %+v", resp)
	}

	// Verify it's recognized as a connection-closing error
	if !ShouldCloseConnection(resp.Error) {
		t.Errorf("Protocol error should require closing connection, got: %T", resp.Error)
	}

	t.Logf("Got expected error: %v", resp.Error)
}

// TestIntegration_EmptyKey tests that empty keys are rejected
func TestIntegration_EmptyKey(t *testing.T) {
	conn, r := dialMemcached(t)

	// Try to get with empty key - send raw command to bypass validation
	_, err := conn.Write([]byte("mg \r\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should get CLIENT_ERROR or ERROR
	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if !resp.HasError() {
		t.Fatalf("Expected error response for empty key, got: %+v", resp)
	}

	if !ShouldCloseConnection(resp.Error) {
		t.Errorf("Empty key error should require closing connection")
	}

	t.Logf("Got expected error: %v", resp.Error)
}

// TestIntegration_BatchWithErrors tests that invalid keys are rejected client-side in batches
func TestIntegration_BatchWithErrors(t *testing.T) {
	conn, r := dialMemcached(t)

	// Send a batch with mixed valid and invalid requests
	// First, a valid set
	req1 := NewRequest(CmdSet, "valid_key", []byte("value"), []Flag{
		{Type: FlagTTL, Token: "60"}, // T60 - set TTL to 60 seconds
	})
	err := WriteRequest(conn, req1)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	// Attempt to send an invalid request with key too long
	longKey := strings.Repeat("a", MaxKeyLength+1)
	req2 := NewRequest(CmdGet, longKey, nil, []Flag{
		{Type: FlagReturnValue}, // v - return value
	})
	err = WriteRequest(conn, req2)
	if err == nil {
		t.Fatal("WriteRequest should fail for invalid key, but succeeded")
	}

	// Verify we get a meaningful error message
	if !strings.Contains(err.Error(), "maximum length") {
		t.Errorf("Expected error about maximum length, got: %v", err)
	}

	// Read first response - should succeed
	resp1, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse 1 failed: %v", err)
	}
	if !resp1.IsSuccess() {
		t.Errorf("First request should succeed, got status=%s", resp1.Status)
	}
}

// TestIntegration_ErrorTypes verifies different error types are correctly identified
func TestIntegration_ErrorTypes(t *testing.T) {
	testCases := []struct {
		name        string
		rawCommand  string
		expectError bool
		errorType   string
		shouldClose bool
	}{
		{
			name:        "Generic ERROR",
			rawCommand:  "UNKNOWN_COMMAND\r\n",
			expectError: true,
			errorType:   "*meta.GenericError",
			shouldClose: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			conn, r := dialMemcached(t)

			// Send raw command
			_, err := conn.Write([]byte(tc.rawCommand))
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			// Read response
			resp, err := ReadResponse(r)
			if err != nil {
				t.Fatalf("ReadResponse failed: %v", err)
			}

			if tc.expectError {
				if !resp.HasError() {
					t.Fatalf("Expected error response, got: %+v", resp)
				}

				// Check error type
				errTypeName := ""
				switch resp.Error.(type) {
				case *GenericError:
					errTypeName = "*meta.GenericError"
				case *ClientError:
					errTypeName = "*meta.ClientError"
				case *ServerError:
					errTypeName = "*meta.ServerError"
				default:
					errTypeName = "unknown"
				}

				if errTypeName != tc.errorType {
					t.Errorf("Expected error type %s, got %s", tc.errorType, errTypeName)
				}

				// Check connection close requirement
				shouldClose := ShouldCloseConnection(resp.Error)
				if shouldClose != tc.shouldClose {
					t.Errorf("Expected shouldClose=%v, got %v", tc.shouldClose, shouldClose)
				}
			} else {
				if resp.HasError() {
					t.Fatalf("Expected successful response, got error: %v", resp.Error)
				}
			}
		})
	}
}
