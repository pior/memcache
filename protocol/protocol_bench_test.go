package protocol

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func BenchmarkReadResponse(b *testing.B) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "SimpleStatus",
			input: "HD\r\n",
		},
		{
			name:  "StatusWithFlags",
			input: "HD f30 c456 O789\r\n",
		},
		{
			name:  "SmallValue",
			input: "VA 5 s5\r\nhello\r\n",
		},
		{
			name:  "MediumValue",
			input: "VA 1024 s1024\r\n" + strings.Repeat("x", 1024) + "\r\n",
		},
		{
			name:  "LargeValue",
			input: "VA 102400 s102400\r\n" + strings.Repeat("x", 100*1024) + "\r\n",
		},
		{
			name:  "ValueWithManyFlags",
			input: "VA 5 f30 c456 t789 s5 O123\r\nhello\r\n",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				reader := bufio.NewReader(strings.NewReader(tt.input))
				ReadResponse(reader)
			}
		})
	}
}

// Initial
// BenchmarkCommandToProtocol/Get-11                    	15712794	        75.78 ns/op	     136 B/op	       3 allocs/op
// BenchmarkCommandToProtocol/Set_SmallValue-11         	14082840	        85.84 ns/op	     136 B/op	       3 allocs/op
// BenchmarkCommandToProtocol/Set_LargeValue-11         	  131455	         9325 ns/op	  106643 B/op	       5 allocs/op
// BenchmarkCommandToProtocol/Delete-11                 	21261217	        54.35 ns/op	     112 B/op	       2 allocs/op
// BenchmarkCommandToProtocol/Increment-11              	10305688	        116.2 ns/op	     216 B/op	       5 allocs/op
// BenchmarkCommandToProtocol/Decrement-11              	10329028	        116.3 ns/op	     216 B/op	       5 allocs/op

// With buffer pool and without flag sorting
// BenchmarkWriteCommand/Get-11                 	36974052	        32.66 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Get_WithFlags-11         	17609260	        67.80 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Set_SmallValue-11        	27187123	        43.74 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Set_LargeValue-11        	  292744	         4276 ns/op	       8 B/op	       1 allocs/op
// BenchmarkWriteCommand/Delete-11                	47322577	        26.00 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Increment-11             	28472593	        42.56 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Decrement-11             	27933285	        42.25 ns/op	       0 B/op	       0 allocs/op

// Using buffer pool passed as input
// BenchmarkWriteCommand/Get-11                 	71321376	        17.41 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Get_WithFlags-11         	21840866	        54.06 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Set_SmallValue-11        	37368188	        30.61 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Set_LargeValue-11        	 1000000	         1067 ns/op	       8 B/op	       1 allocs/op
// BenchmarkWriteCommand/Delete-11                	100000000	        12.32 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Increment-11             	43265460	        28.84 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Decrement-11             	43178604	        28.87 ns/op	       0 B/op	       0 allocs/op
func BenchmarkWriteCommand(b *testing.B) {
	largeValue := make([]byte, 100*1024) // 100KB

	tests := []struct {
		name string
		cmd  *Command
	}{
		{
			name: "Get",
			cmd:  newGetCommand("test_key"),
		},
		{
			name: "Get_WithFlags",
			cmd: newGetCommand("test_key").
				WithFlag("b", "").
				WithFlag("c", "").
				WithFlag("f", "").
				WithFlag("h", "").
				WithFlag("k", "").
				WithFlag("l", "").
				WithFlag("O", "123"),
		},
		{
			name: "Set_SmallValue",
			cmd:  newSetCommand("test_key", []byte("small_value"), 300),
		},
		{
			name: "Set_LargeValue",
			cmd:  newSetCommand("test_key", largeValue, 300),
		},
		{
			name: "Delete",
			cmd:  newDeleteCommand("test_key"),
		},
		{
			name: "Increment",
			cmd:  newIncrementCommand("counter_key", 1),
		},
		{
			name: "Decrement",
			cmd:  newDecrementCommand("counter_key", 1),
		},
	}

	var buf bytes.Buffer

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				WriteCommand(tt.cmd, &buf)
				buf.Reset()
			}
		})
	}
}
