// Experiment: Meta Arithmetic (ma) behaviors
//
// Purpose: Test the ma command for increment/decrement operations
//
// Test cases:
// 1. ma increment (default mode, default delta=1)
// 2. ma with 'v' flag to return new value
// 3. ma with 'D' flag for custom delta
// 4. ma with 'MI' mode (increment explicit)
// 5. ma with 'MD' mode (decrement)
// 6. ma decrement to zero (should not underflow)
// 7. ma on non-existent key (should return NF)
// 8. ma with 'N' flag (auto-create on miss)
// 9. ma with 'N' and 'J' flags (auto-create with initial value)
// 10. ma with 'c' flag (return CAS)
// 11. ma with 'C' flag (CAS check)
// 12. ma with 'T' flag (update TTL)
// 13. ma with 't' flag (return TTL)

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

	// Setup: create a numeric value
	fmt.Println("=== Setup ===")
	writer.WriteString("ms counter 2\r\n10\r\n")
	writer.Flush()
	response, _ := reader.ReadString('\n')
	fmt.Printf("Setup response: %s", response)

	// Test 1: ma increment (default mode, default delta=1)
	fmt.Println("\n=== Test 1: ma increment (default, delta=1) ===")
	cmd := "ma counter\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 2: ma with 'v' flag to return new value
	fmt.Println("\n=== Test 2: ma with 'v' flag (return value) ===")
	cmd = "ma counter v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 3: ma with 'D' flag for custom delta
	fmt.Println("\n=== Test 3: ma with D5 (delta=5) ===")
	cmd = "ma counter v D5\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 4: ma with 'MI' mode (increment explicit)
	fmt.Println("\n=== Test 4: ma with MI mode (increment explicit) ===")
	cmd = "ma counter v MI D3\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 5: ma with 'MD' mode (decrement)
	fmt.Println("\n=== Test 5: ma with MD mode (decrement) ===")
	cmd = "ma counter v MD D7\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 6: ma decrement to zero (should not underflow)
	fmt.Println("\n=== Test 6: ma decrement below zero (should stop at 0) ===")
	cmd = "ma counter v MD D100\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 7: ma on non-existent key (should return NF)
	fmt.Println("\n=== Test 7: ma on non-existent key (should return NF) ===")
	cmd = "ma nonexistent v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 8: ma with 'N' flag (auto-create on miss with TTL=60)
	fmt.Println("\n=== Test 8: ma with N60 flag (auto-create on miss) ===")
	cmd = "ma autocreate v N60\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Increment it
	writer.WriteString("ma autocreate v D5\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	value, _ := reader.ReadString('\n')
	fmt.Printf("After increment: %s%s", response, value)

	// Test 9: ma with 'N' and 'J' flags (auto-create with initial value=100)
	fmt.Println("\n=== Test 9: ma with N60 J100 (auto-create with initial=100) ===")
	cmd = "ma withseed v N60 J100\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 10: ma with 'c' flag (return CAS)
	fmt.Println("\n=== Test 10: ma with 'c' flag (return CAS) ===")
	cmd = "ma counter v c\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Get CAS for test 11
	writer.WriteString("mg counter c\r\n")
	writer.Flush()
	response, _ = reader.ReadString('\n')
	var casValue string
	fmt.Sscanf(response, "HD c%s", &casValue)
	fmt.Printf("Current CAS: %s\n", casValue)

	// Test 11: ma with 'C' flag (CAS check)
	fmt.Println("\n=== Test 11: ma with CAS check ===")
	cmd = fmt.Sprintf("ma counter v C%s\r\n", casValue)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test invalid CAS
	fmt.Println("\n=== Test 11b: ma with invalid CAS (should return EX) ===")
	cmd = "ma counter v C999999\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 12: ma with 'T' flag (update TTL)
	fmt.Println("\n=== Test 12: ma with T30 (update TTL) ===")
	cmd = "ma counter v t T30\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 13: ma with 't' flag (return TTL)
	fmt.Println("\n=== Test 13: ma with 't' flag (return TTL) ===")
	cmd = "ma counter v t\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	fmt.Println("\n=== Done ===")
}
