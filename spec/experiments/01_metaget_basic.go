// Experiment: Meta Get (mg) basic behavior
//
// Purpose: Test the basic mg command with different flag combinations
// to understand response structure and behavior
//
// Test cases:
// 1. mg with no flags (minimal response)
// 2. mg with 'v' flag (return value)
// 3. mg with 'k' flag (return key)
// 4. mg with 'c' flag (return CAS)
// 5. mg with 'f' flag (return client flags)
// 6. mg with 's' flag (return size)
// 7. mg with 't' flag (return TTL)
// 8. mg with multiple flags
// 9. mg on non-existent key (miss behavior)
// 10. mg on non-existent key with 'q' flag (quiet mode)

package main

import (
	"bufio"
	"fmt"
	"net"
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

	// First, set a test value
	fmt.Println("=== Setting up test data ===")
	cmd := "ms testkey 5 F30 T60\r\nvalue\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ := reader.ReadString('\n')
	fmt.Printf("Set response: %s", response)

	// Test 1: mg with no flags
	fmt.Println("\n=== Test 1: mg with no flags ===")
	cmd = "mg testkey\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 2: mg with 'v' flag (value)
	fmt.Println("\n=== Test 2: mg with 'v' flag ===")
	cmd = "mg testkey v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 3: mg with 'k' flag (key)
	fmt.Println("\n=== Test 3: mg with 'k' flag ===")
	cmd = "mg testkey k\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 4: mg with 'c' flag (CAS)
	fmt.Println("\n=== Test 4: mg with 'c' flag ===")
	cmd = "mg testkey c\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 5: mg with 'f' flag (client flags)
	fmt.Println("\n=== Test 5: mg with 'f' flag ===")
	cmd = "mg testkey f\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 6: mg with 's' flag (size)
	fmt.Println("\n=== Test 6: mg with 's' flag ===")
	cmd = "mg testkey s\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 7: mg with 't' flag (TTL)
	fmt.Println("\n=== Test 7: mg with 't' flag ===")
	cmd = "mg testkey t\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 8: mg with multiple flags
	fmt.Println("\n=== Test 8: mg with multiple flags (k f c t s v) ===")
	cmd = "mg testkey k f c t s v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 9: mg on non-existent key
	fmt.Println("\n=== Test 9: mg on non-existent key ===")
	cmd = "mg nonexistent v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 10: mg on non-existent key with 'q' flag
	fmt.Println("\n=== Test 10: mg on non-existent key with 'q' flag (quiet mode) ===")
	cmd = "mg nonexistent v q\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	// With 'q' flag on miss, there should be no response
	// Let's try with a no-op command to see when responses end
	cmd2 := "mn\r\n"
	writer.WriteString(cmd2)
	writer.Flush()

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	response, err = reader.ReadString('\n')
	conn.SetReadDeadline(time.Time{}) // clear deadline
	if err != nil {
		fmt.Printf("Request: %sNo response (as expected with 'q' flag on miss)\n", cmd)
	} else {
		fmt.Printf("Request: %sResponse: %s", cmd, response)
	}

	fmt.Println("\n=== Done ===")
}
