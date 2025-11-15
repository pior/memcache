// Experiment: Edge cases and error conditions
//
// Purpose: Test edge cases, error conditions, and protocol boundaries
//
// Test cases:
// 1. Empty key (should return CLIENT_ERROR)
// 2. Key with spaces (should return CLIENT_ERROR or handle as protocol error)
// 3. Very long key (250 chars - max allowed)
// 4. Too long key (251+ chars - should error)
// 5. Value size mismatch (declared vs actual)
// 6. Zero-length value
// 7. Large value
// 8. Invalid command (should return ERROR or CLIENT_ERROR)
// 9. Missing required parameters
// 10. Invalid flag syntax
// 11. Opaque token too long (>32 bytes)
// 12. Base64 encoded key (b flag)
// 13. TTL edge cases (0, -1, very large)
// 14. Multiple commands in pipeline
// 15. Meta noop (mn) command

package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:11211")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Test 1: Empty key (should return CLIENT_ERROR)
	fmt.Println("=== Test 1: Empty key ===")
	cmd := "mg  v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	response, _ := reader.ReadString('\n')
	conn.SetReadDeadline(time.Time{})
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 3: Very long key (250 chars - max allowed)
	fmt.Println("\n=== Test 3: Very long key (250 chars) ===")
	longKey := strings.Repeat("a", 250)
	cmd = fmt.Sprintf("ms %s 5\r\nhello\r\n", longKey)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: ms %s 5\\r\\nhello\\r\\n\nResponse: %s", longKey[:20]+"...(250 total)", response)

	// Verify it can be retrieved
	cmd = fmt.Sprintf("mg %s v\r\n", longKey)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Retrieve: mg %s v\\r\\n\nResponse: %s", longKey[:20]+"...(250 total)", response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 4: Too long key (251 chars - should error)
	fmt.Println("\n=== Test 4: Too long key (251 chars) ===")
	tooLongKey := strings.Repeat("b", 251)
	cmd = fmt.Sprintf("ms %s 5\r\nhello\r\n", tooLongKey)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: ms %s 5\\r\\nhello\\r\\n\nResponse: %s", tooLongKey[:20]+"...(251 total)", response)

	// Test 6: Zero-length value
	fmt.Println("\n=== Test 6: Zero-length value ===")
	cmd = "ms emptyval 0\r\n\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Verify
	cmd = "mg emptyval v s\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Verify: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: '%s'\n", strings.TrimRight(value, "\r\n"))
	}

	// Test 7: Large value (1MB)
	fmt.Println("\n=== Test 7: Large value (1MB) ===")
	largeValue := strings.Repeat("x", 1024*1024)
	cmd = fmt.Sprintf("ms largekey %d\r\n%s\r\n", len(largeValue), largeValue)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: ms largekey %d\\r\\n<1MB data>\\r\\n\nResponse: %s", len(largeValue), response)

	// Verify size
	cmd = "mg largekey s\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Verify size: %sResponse: %s", cmd, response)

	// Clean up large value
	writer.WriteString("md largekey\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 8: Invalid command
	fmt.Println("\n=== Test 8: Invalid command ===")
	cmd = "xx invalidcmd\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 12: Base64 encoded key
	fmt.Println("\n=== Test 12: Base64 encoded key ===")
	// "hello" in base64 is "aGVsbG8="
	cmd = "ms aGVsbG8= 5 b\r\nworld\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Retrieve with base64
	cmd = "mg aGVsbG8= b v k\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Retrieve: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 13: TTL edge cases
	fmt.Println("\n=== Test 13a: TTL=0 (no expiration) ===")
	cmd = "ms ttl0 5 T0 t\r\nhello\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	cmd = "mg ttl0 t\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Verify TTL: %sResponse: %s", cmd, response)

	fmt.Println("\n=== Test 13b: TTL=-1 (explicit infinite) ===")
	cmd = "ms ttlneg1 5 T-1 t\r\nhello\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	cmd = "mg ttlneg1 t\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Verify TTL: %sResponse: %s", cmd, response)

	// Test 14: Multiple commands in pipeline
	fmt.Println("\n=== Test 14: Pipeline multiple commands ===")
	pipeline := "ms p1 2 O1\r\nv1\r\nms p2 2 O2\r\nv2\r\nmg p1 v O3\r\nmg p2 v O4\r\n"
	writer.WriteString(pipeline)
	writer.Flush()

	fmt.Println("Pipeline:")
	fmt.Println("  ms p1 2 O1")
	fmt.Println("  ms p2 2 O2")
	fmt.Println("  mg p1 v O3")
	fmt.Println("  mg p2 v O4")
	fmt.Println("Responses:")

	for i := 0; i < 4; i++ {
		response, _ = reader.ReadString('\n')
		fmt.Printf("  %d: %s", i+1, response)
		if response[:2] == "VA" {
			value, _ := reader.ReadString('\n')
			fmt.Printf("     Value: %s", value)
		}
	}

	// Test 15: Meta noop
	fmt.Println("\n=== Test 15: Meta noop (mn) ===")
	cmd = "mn\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	fmt.Println("\n=== Done ===")
}
