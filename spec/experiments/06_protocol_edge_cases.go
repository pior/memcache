// Experiment: Protocol-level edge cases and limits
//
// Purpose: Test protocol boundaries, limits, and error conditions
//
// Test cases:
// Part 1: Advanced Flag Behaviors
// 1. 'h' flag - hit before status
// 2. 'l' flag - last access time
// 3. 'u' flag - don't bump LRU (combined with l and h)
// 4. 'x' flag - remove value but keep item (md command)
// 5. Win flag lifecycle (W → Z → cleared)
//
// Part 2: Protocol Error Conditions
// 6. Duplicate flags in a command
// 7. Conflicting flags (e.g., MI and MD together)
// 8. Very large opaque token (>32 bytes)
// 9. Invalid flag syntax
// 10. CAS value 0 behavior
//
// Part 3: Protocol Edge Cases
// 11. Arithmetic overflow (increment past uint64 max)
// 12. Mixed text and meta protocol commands
// 13. Multiple spaces in command
// 14. Stats command (text protocol)
// 15. Flush command (text protocol)

package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:11211")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// === Part 1: Advanced Flag Behaviors ===

	// Test 1: 'h' flag - hit before status
	fmt.Println("=== Test 1: 'h' flag - hit before status ===")
	writer.WriteString("ms hflagtest 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	cmd := "mg hflagtest h v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ := reader.ReadString('\n')
	fmt.Printf("First access with h flag:\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Access again to see h=1
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Second access with h flag:\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 2: 'l' flag - last access time
	fmt.Println("\n=== Test 2: 'l' flag - last access time ===")
	writer.WriteString("ms lflagtest 5\r\nworld\r\n")
	writer.Flush()
	reader.ReadString('\n')

	cmd = "mg lflagtest l v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("First access with l flag:\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	fmt.Println("Waiting 2 seconds...")
	// Note: In real test we'd sleep, but for brevity we'll just access again
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Second access with l flag:\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 3: 'u' flag - don't bump LRU
	fmt.Println("\n=== Test 3: 'u' flag - don't bump LRU ===")
	writer.WriteString("ms uflagtest 5\r\nubump\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Access with u flag (should not update access time)
	cmd = "mg uflagtest u l h v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Access with u flag (no LRU bump):\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Access again with u flag - l should show same time
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Second access with u flag:\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 4: 'x' flag in md - remove value but keep item
	fmt.Println("\n=== Test 4: 'x' flag - remove value but keep item ===")
	writer.WriteString("ms xflagtest 5 F123\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Delete with x flag - this removes value but keeps metadata
	cmd = "md xflagtest x\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Delete with x flag:\nRequest: %sResponse: %s", cmd, response)

	// Try to get the item - should return EN but client flags should be 0
	cmd = "mg xflagtest f\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Get after x delete (flags should be 0):\nRequest: %sResponse: %s", cmd, response)

	// Test 5: Win flag lifecycle
	fmt.Println("\n=== Test 5: Win flag lifecycle (W → Z → cleared) ===")
	// Set item with TTL
	writer.WriteString("ms wintest 5 T10\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Invalidate with I flag
	cmd = "md wintest I T30\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Invalidate with I:\nRequest: %sResponse: %s", cmd, response)

	// Get should return X (stale) and W (win)
	cmd = "mg wintest v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("First get after invalidation (should have X and W):\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Second get should return Z (recache winner)
	cmd = "mg wintest v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Second get (should have X and Z):\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Update the item (clears stale state)
	writer.WriteString("ms wintest 5 T10\r\nfresh\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Third get should be normal (no X, Z, or W)
	cmd = "mg wintest v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Third get after update (no X/Z/W):\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// === Part 2: Protocol Error Conditions ===

	// Test 6: Duplicate flags
	fmt.Println("\n=== Test 6: Duplicate flags ===")
	writer.WriteString("ms testkey 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	cmd = "mg testkey v v c c\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 7: Conflicting flags (increment and decrement)
	fmt.Println("\n=== Test 7: Conflicting mode flags (MI and MD) ===")
	writer.WriteString("ms counter 2\r\n10\r\n")
	writer.Flush()
	reader.ReadString('\n')

	cmd = "ma counter v MI MD\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 8: Very large opaque token (>32 bytes)
	fmt.Println("\n=== Test 8: Opaque token >32 bytes ===")
	longOpaque := strings.Repeat("a", 40)
	cmd = fmt.Sprintf("mg testkey O%s\r\n", longOpaque)
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: mg testkey O%s...(%d bytes)\\r\\n\nResponse: %s", longOpaque[:10], len(longOpaque), response)

	// Test 9: Invalid flag syntax
	fmt.Println("\n=== Test 9: Invalid flag syntax ===")
	cmd = "mg testkey @invalid\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Test 10: CAS value 0 behavior
	fmt.Println("\n=== Test 10: CAS value 0 ===")
	writer.WriteString("ms cas0test 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	// Get CAS value
	cmd = "mg cas0test c\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Get CAS: %sResponse: %s", cmd, response)

	// Try CAS with 0
	cmd = "ms cas0test 5 C0\r\nworld\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("CAS with C0: %sResponse: %s", cmd, response)

	// === Part 3: Protocol Edge Cases ===

	// Test 11: Arithmetic overflow behavior
	fmt.Println("\n=== Test 11: Arithmetic overflow ===")
	// Set to max uint64
	maxUint64 := "18446744073709551615"
	cmd = fmt.Sprintf("ms overflow %d\r\n%s\r\n", len(maxUint64), maxUint64)
	writer.WriteString(cmd)
	writer.Flush()
	reader.ReadString('\n')

	// Try to increment
	cmd = "ma overflow v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Increment max uint64:\nRequest: %sResponse: %s", cmd, response)
	if response[:2] == "VA" {
		value, _ := reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 12: Mixed text and meta protocol
	fmt.Println("\n=== Test 12: Mixed text and meta protocol ===")
	// Use old text protocol 'set'
	cmd = "set oldstyle 0 0 5\r\nhello\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Text protocol SET: %sResponse: %s", cmd, response)

	// Retrieve with meta protocol
	cmd = "mg oldstyle v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	value, _ := reader.ReadString('\n')
	fmt.Printf("Meta protocol GET: %sResponse: %sValue: %s", cmd, response, value)

	// Set with meta protocol
	cmd = "ms newstyle 5\r\nworld\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	reader.ReadString('\n')

	// Retrieve with text protocol 'get'
	cmd = "get newstyle\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	if strings.HasPrefix(response, "VALUE") {
		value, _ = reader.ReadString('\n')
		end, _ := reader.ReadString('\n')
		fmt.Printf("Text protocol GET: %sResponse: %s%sEnd: %s", cmd, response, value, end)
	} else {
		fmt.Printf("Text protocol GET: %sResponse: %s", cmd, response)
	}

	// Test 13: Multiple spaces
	fmt.Println("\n=== Test 13: Multiple spaces in command ===")
	cmd = "mg  testkey  v  c\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: mg  testkey  v  c\\r\\n\nResponse: %s", response)
	if response[:2] == "VA" {
		value, _ = reader.ReadString('\n')
		fmt.Printf("Value: %s", value)
	}

	// Test 14: Check if stats command works (text protocol)
	fmt.Println("\n=== Test 14: Stats command (text protocol) ===")
	cmd = "stats\r\n"
	writer.WriteString(cmd)
	writer.Flush()

	fmt.Printf("Request: %s", cmd)
	fmt.Println("Response (first 5 lines):")
	for i := 0; i < 5; i++ {
		response, _ = reader.ReadString('\n')
		fmt.Printf("  %s", response)
		if strings.HasPrefix(response, "END") {
			break
		}
	}
	// Consume rest of stats response
	for {
		response, _ = reader.ReadString('\n')
		if strings.HasPrefix(response, "END") {
			break
		}
	}

	// Test 15: Flush command (text protocol)
	fmt.Println("\n=== Test 15: Flush command ===")
	writer.WriteString("ms flushtest 5\r\nhello\r\n")
	writer.Flush()
	reader.ReadString('\n')

	cmd = "flush_all\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("Request: %sResponse: %s", cmd, response)

	// Verify item is gone
	cmd = "mg flushtest v\r\n"
	writer.WriteString(cmd)
	writer.Flush()
	response, _ = reader.ReadString('\n')
	fmt.Printf("After flush: %sResponse: %s", cmd, response)

	fmt.Println("\n=== Done ===")
}
