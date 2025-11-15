// Experiment: Meta Set (ms) modes and behaviors
//
// Purpose: Test the ms command with different mode flags and behaviors
//
// Test cases:
// 1. Basic ms (default = set mode)
// 2. ms with MS mode (explicit set)
// 3. ms with ME mode (add - only if not exists)
// 4. ms with MR mode (replace - only if exists)
// 5. ms with MA mode (append)
// 6. ms with MP mode (prepend)
// 7. ms with CAS (compare and swap)
// 8. ms with invalid CAS (should return EX)
// 9. ms replace on non-existent key (should return NF)
// 10. ms add on existing key (should return NS)
// 11. ms with 'c' flag to return CAS value
// 12. ms with 'q' flag (quiet mode on success)
// 13. ms with 'O' flag (opaque)

package main

import (
	"bufio"
	"fmt"
	"net"
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:11211")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Clean up before starting
	fmt.Println("=== Cleanup ===")
	writer.WriteString("md testkey\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 1: Basic ms (default set mode)
	fmt.Println("\n=== Test 1: Basic ms (set mode) ===")
	cmd := "ms testkey 6\r\nvalue1\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ := reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 2: ms with MS mode (explicit set)
	fmt.Println("\n=== Test 2: ms with MS mode (explicit set) ===")
	cmd = "ms testkey 6 MS\r\nvalue2\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Clean up for add test
	writer.WriteString("md testkey\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 3: ms with ME mode (add - only if not exists)
	fmt.Println("\n=== Test 3: ms with ME mode (add) - first time ===")
	cmd = "ms testkey 6 ME\r\nvalue3\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 10: ms add on existing key (should return NS)
	fmt.Println("\n=== Test 10: ms add on existing key (should return NS) ===")
	cmd = "ms testkey 6 ME\r\nvalue4\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 4: ms with MR mode (replace - only if exists)
	fmt.Println("\n=== Test 4: ms with MR mode (replace) ===")
	cmd = "ms testkey 6 MR\r\nvalue5\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 9: ms replace on non-existent key
	writer.WriteString("md testkey\r\n")
	writer.Flush()
	reader.ReadString('\n')

	fmt.Println("\n=== Test 9: ms replace on non-existent key (should return NF) ===")
	cmd = "ms testkey 6 MR\r\nvalue6\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Setup for append/prepend
	writer.WriteString("ms testkey 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 5: ms with MA mode (append)
	fmt.Println("\n=== Test 5: ms with MA mode (append) ===")
	cmd = "ms testkey 6 MA\r\nworld!\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Verify append result
	writer.WriteString("mg testkey v\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	value, _ := reader.ReadString('\n')
	fmt.Printf("Verification - mg testkey v:\nResponse: %sValue: %s", response, value)

	// Reset for prepend test
	writer.WriteString("ms testkey 5\r\nworld\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 6: ms with MP mode (prepend)
	fmt.Println("\n=== Test 6: ms with MP mode (prepend) ===")
	cmd = "ms testkey 6 MP\r\nhello \r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Verify prepend result
	writer.WriteString("mg testkey v\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	value, _ = reader.ReadString('\n')
	fmt.Printf("Verification - mg testkey v:\nResponse: %sValue: %s", response, value)

	// Test 7: ms with CAS (get CAS first)
	fmt.Println("\n=== Test 7: ms with CAS (compare and swap) ===")
	writer.WriteString("mg testkey c\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Get CAS: %s", response)

	// Extract CAS value (assuming format "HD c<number>")
	var casValue string
	fmt.Sscanf(response, "HD c%s", &casValue)

	cmd = fmt.Sprintf("ms testkey 6 C%s\r\nvalue7\r\n", casValue)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 8: ms with invalid CAS (should return EX)
	fmt.Println("\n=== Test 8: ms with invalid CAS (should return EX) ===")
	cmd = "ms testkey 6 C999999\r\nvalue8\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 11: ms with 'c' flag to return CAS
	fmt.Println("\n=== Test 11: ms with 'c' flag to return CAS ===")
	cmd = "ms testkey 6 c\r\nvalue9\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 12: ms with 'q' flag (quiet mode on success)
	fmt.Println("\n=== Test 12: ms with 'q' flag (quiet mode) ===")
	cmd = "ms testkey 7 q\r\nvalue10\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	// With 'q' flag on success, there should be no response
	// Send mn to detect end
	writer.WriteString("mn\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse after 'q' (should be MN): %s", cmd, response)

	// Test 13: ms with 'O' flag (opaque)
	fmt.Println("\n=== Test 13: ms with 'O' flag (opaque) ===")
	cmd = "ms testkey 7 O123\r\nvalue11\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	fmt.Println("\n=== Done ===")
}
