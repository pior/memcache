package meta

import (
	"errors"
	"fmt"
)

// Error types for meta protocol operations.
// These errors help clients determine appropriate error handling strategy,
// particularly regarding connection management (close vs. retry).

// ClientError represents a CLIENT_ERROR response from memcached.
// CRITICAL: When this error occurs, the connection MUST be closed as the
// protocol state may be corrupted. The server detected invalid client input
// and the parsing state is undefined.
//
// Common causes:
//   - Key length > 250 bytes
//   - Opaque token > 32 bytes
//   - Invalid flag syntax
//   - Size mismatch in data block
//   - Conflicting mode flags
//   - Non-numeric value for arithmetic operations
//
// Connection handling: CLOSE connection immediately
type ClientError struct {
	Message string
}

func (e *ClientError) Error() string {
	return "CLIENT_ERROR: " + e.Message
}

// ShouldCloseConnection returns true - client errors require closing connection
func (e *ClientError) ShouldCloseConnection() bool {
	return true
}

// ServerError represents a SERVER_ERROR response from memcached.
// Indicates a server-side error condition. The connection protocol state
// is still valid, but the operation failed due to server issues.
//
// Common causes:
//   - Out of memory
//   - Internal server error
//   - Configuration issues
//
// Connection handling: Connection can be REUSED, operation may be retried
type ServerError struct {
	Message string
}

func (e *ServerError) Error() string {
	return "SERVER_ERROR: " + e.Message
}

// ShouldCloseConnection returns false - server errors don't corrupt protocol state
func (e *ServerError) ShouldCloseConnection() bool {
	return false
}

// GenericError represents a generic ERROR response from memcached.
// Typically indicates unknown command or protocol violation.
//
// Common causes:
//   - Unknown command
//   - Protocol violation
//   - Unexpected input
//
// Connection handling: Connection should be CLOSED as protocol state is uncertain
type GenericError struct {
	Message string
}

func (e *GenericError) Error() string {
	return e.Message
}

// ShouldCloseConnection returns true - generic errors indicate protocol issues
func (e *GenericError) ShouldCloseConnection() bool {
	return true
}

// InvalidKeyError is returned when a key fails validation.
// Indicates the key violates memcache protocol constraints before sending to server.
//
// Common causes:
//   - Empty key
//   - Key exceeds 250 bytes
//   - Key contains whitespace (without base64 flag)
//
// Connection handling: Connection is still valid, operation was rejected client-side
type InvalidKeyError struct {
	Message string
}

func (e *InvalidKeyError) Error() string {
	return e.Message
}

// ParseError represents a client-side parsing error.
// Indicates the client failed to parse the server response, which suggests
// either a protocol violation by the server or a bug in the client parser.
//
// Common causes:
//   - Malformed response line
//   - Invalid size in VA response
//   - Missing data block
//   - Unexpected EOF
//
// Connection handling: Connection should be CLOSED as state is uncertain
type ParseError struct {
	Message string
	Err     error // Underlying error, if any
}

func (e *ParseError) Error() string {
	if e.Err != nil {
		return "parse error: " + e.Message + ": " + e.Err.Error()
	}
	return "parse error: " + e.Message
}

// Unwrap returns the underlying error for error chain inspection
func (e *ParseError) Unwrap() error {
	return e.Err
}

// ShouldCloseConnection returns true - parse errors indicate corrupted state
func (e *ParseError) ShouldCloseConnection() bool {
	return true
}

// ConnectionError wraps underlying I/O errors from connection operations.
// Used to distinguish network/connection issues from protocol errors.
//
// Common causes:
//   - Connection closed
//   - Network timeout
//   - Connection reset
//   - Write buffer full
//
// Connection handling: Connection is already broken, CLOSE and potentially RECONNECT
type ConnectionError struct {
	Op  string // Operation that failed (read, write, etc.)
	Err error  // Underlying error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection error during %s: %v", e.Op, e.Err)
}

// Unwrap returns the underlying error for error chain inspection
func (e *ConnectionError) Unwrap() error {
	return e.Err
}

// ShouldCloseConnection returns true - connection errors mean connection is broken
func (e *ConnectionError) ShouldCloseConnection() bool {
	return true
}

// ErrorWithConnectionState is an interface for errors that indicate
// whether the connection should be closed.
// Implemented by all protocol error types.
type ErrorWithConnectionState interface {
	error
	ShouldCloseConnection() bool
}

// ShouldCloseConnection is a helper function to determine if an error
// requires closing the connection.
//
// Returns true for:
//   - ClientError
//   - GenericError
//   - ParseError
//   - ConnectionError
//
// Returns false for:
//   - ServerError
//   - nil
//
// Usage:
//
//	resp, err := ReadResponse(r)
//	if err != nil {
//	    if ShouldCloseConnection(err) {
//	        conn.Close()
//	    }
//	    return err
//	}
func ShouldCloseConnection(err error) bool {
	if err == nil {
		return false
	}

	var e ErrorWithConnectionState
	if errors.As(err, &e) {
		return e.ShouldCloseConnection()
	}

	// Unknown error type - be conservative and close connection
	return true
}
