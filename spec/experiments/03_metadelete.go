// Experiment: Meta Delete (md) behaviors
//
// Purpose: Test the md command with different flags and modes
//
// Test cases:
// 1. Basic md (permanent delete)
// 2. md with 'k' flag (return key)
// 3. md with 'O' flag (opaque)
// 4. md on non-existent key (should return NF)
// 5. md with 'I' flag (invalidate - mark as stale)
// 6. mg after invalidation (should return X flag)
// 7. md with 'I' and 'T' flags (invalidate with new TTL)
// 8. md with 'C' flag (CAS check)
// 9. md with invalid CAS (should return EX)
// 10. md with 'q' flag (quiet mode)

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

	// Setup test data
	fmt.Println("=== Setup ===")
	writer.WriteString("ms testkey 5\r\nhello\r\n")
	writer.Flush()
	response, _ := reader.ReadString('\n')
	fmt.Printf("Setup response: %s", response)

	// Test 1: Basic md (permanent delete)
	fmt.Println("\n=== Test 1: Basic md (permanent delete) ===")
	cmd := "md testkey\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Verify deletion
	writer.WriteString("mg testkey v\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Verification - mg testkey v: %s", response)

	// Test 4: md on non-existent key
	fmt.Println("\n=== Test 4: md on non-existent key (should return NF) ===")
	cmd = "md nonexistent\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Setup for test 2
	writer.WriteString("ms testkey2 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 2: md with 'k' flag
	fmt.Println("\n=== Test 2: md with 'k' flag (return key) ===")
	cmd = "md testkey2 k\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Setup for test 3
	writer.WriteString("ms testkey3 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 3: md with 'O' flag (opaque)
	fmt.Println("\n=== Test 3: md with 'O' flag (opaque) ===")
	cmd = "md testkey3 O456\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Setup for invalidation tests
	writer.WriteString("ms stalekey 7 T300\r\ntesting\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 5: md with 'I' flag (invalidate - mark as stale)
	fmt.Println("\n=== Test 5: md with 'I' flag (invalidate) ===")
	cmd = "md stalekey I\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 6: mg after invalidation (should return X flag for stale)
	fmt.Println("\n=== Test 6: mg after invalidation (should see X flag) ===")
	cmd = "mg stalekey v c\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	value, _ := reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %sValue: %s", cmd, response, value)

	// Setup for TTL test
	writer.WriteString("ms stalekey2 7 T300\r\ntesting\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 7: md with 'I' and 'T' flags (invalidate with new TTL)
	fmt.Println("\n=== Test 7: md with 'I' and 'T' flags (invalidate with TTL=30) ===")
	cmd = "md stalekey2 I T30\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Verify TTL was updated
	writer.WriteString("mg stalekey2 t v\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	value, _ = reader.ReadString('\n')
	fmt.Printf("Verification - mg stalekey2 t v:\nResponse: %sValue: %s", response, value)

	// Setup for CAS tests
	writer.WriteString("ms caskey 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Get CAS value
	writer.WriteString("mg caskey c\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("\n=== Setup for CAS test - Get CAS ===\nResponse: %s", response)

	var casValue string
	fmt.Sscanf(response, "HD c%s", &casValue)

	// Test 8: md with 'C' flag (CAS check)
	fmt.Println("\n=== Test 8: md with correct CAS ===")
	cmd = fmt.Sprintf("md caskey C%s\r\n", casValue)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Setup for invalid CAS test
	writer.WriteString("ms caskey2 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 9: md with invalid CAS (should return EX)
	fmt.Println("\n=== Test 9: md with invalid CAS (should return EX) ===")
	cmd = "md caskey2 C999999\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Setup for quiet mode test
	writer.WriteString("ms quietkey 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Test 10: md with 'q' flag (quiet mode)
	fmt.Println("\n=== Test 10: md with 'q' flag (quiet mode) ===")
	cmd = "md quietkey q\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	// With 'q' flag on success, there should be no response
	// Send mn to detect end
	writer.WriteString("mn\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse after 'q' (should be MN): %s", cmd, response)

	fmt.Println("\n=== Done ===")
}
