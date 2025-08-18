package protocol

import (
	"io"
	"strconv"
)

// BenchmarkCommandToProtocol/Get-11                    	15712794	        75.78 ns/op	     136 B/op	       3 allocs/op
// BenchmarkCommandToProtocol/Set_SmallValue-11         	14082840	        85.84 ns/op	     136 B/op	       3 allocs/op
// BenchmarkCommandToProtocol/Set_LargeValue-11         	  131455	         9325 ns/op	  106643 B/op	       5 allocs/op
// BenchmarkCommandToProtocol/Delete-11                 	21261217	        54.35 ns/op	     112 B/op	       2 allocs/op
// BenchmarkCommandToProtocol/Increment-11              	10305688	        116.2 ns/op	     216 B/op	       5 allocs/op
// BenchmarkCommandToProtocol/Decrement-11              	10329028	        116.3 ns/op	     216 B/op	       5 allocs/op

// BenchmarkWriteCommand/Get-11      			   	21844230	        54.51 ns/op	      24 B/op	       1 allocs/op
// BenchmarkWriteCommand/Get_WithFlags-11         	 8357272	       143.9 ns/op	     104 B/op	       3 allocs/op
// BenchmarkWriteCommand/Set_SmallValue-11        	17826216	        66.14 ns/op	      24 B/op	       1 allocs/op
// BenchmarkWriteCommand/Set_LargeValue-11        	  266272	   	   4025 ns/op	      32 B/op	       2 allocs/op
// BenchmarkWriteCommand/Delete-11                	34799629	        34.73 ns/op	       0 B/op	       0 allocs/op
// BenchmarkWriteCommand/Increment-11             	12331142	        97.55 ns/op	     104 B/op	       3 allocs/op
// BenchmarkWriteCommand/Decrement-11             	12319986	        99.19 ns/op	     104 B/op	       3 allocs/op

var writeCommandBufferPool = newByteBufferPool(1024)

func WriteCommand(cmd *Command, writer io.Writer) (int64, error) {
	var buf = writeCommandBufferPool.Get()
	defer writeCommandBufferPool.Put(buf)

	buf.WriteString(string(cmd.Type))

	if cmd.Type != CmdNoOp {
		buf.WriteByte(' ')
		buf.WriteString(cmd.Key)
	}

	if cmd.Type == CmdSet {
		buf.WriteByte(' ')
		buf.WriteString(strconv.Itoa(len(cmd.Value)))
	}

	for _, flag := range cmd.Flags {
		buf.WriteByte(' ')
		buf.WriteString(string(flag.Type))
		if flag.Value != "" {
			buf.WriteString(flag.Value)
		}
	}

	if cmd.Opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(cmd.Opaque)
	}

	if cmd.Type == CmdSet {
		buf.WriteString("\r\n")
		buf.Write(cmd.Value)
	}

	buf.WriteString("\r\n")

	return io.Copy(writer, buf)
}
