package meta_test

import (
	"bufio"
	"bytes"
	"fmt"
	"log"

	"github.com/pior/memcache/meta"
)

// ExampleWriteRequest demonstrates basic request serialization.
func ExampleWriteRequest() {
	req := meta.NewRequest(meta.CmdGet, "mykey", nil,
		meta.Flag{Type: meta.FlagReturnValue},
	)

	var buf bytes.Buffer
	_, err := meta.WriteRequest(&buf, req)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%q", buf.String())
	// Output: "mg mykey v\r\n"
}

// ExampleReadResponse demonstrates response parsing.
func ExampleReadResponse() {
	input := "VA 5\r\nhello\r\n"
	r := bufio.NewReader(bytes.NewBufferString(input))

	resp, err := meta.ReadResponse(r)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("Data: %s\n", resp.Data)
	// Output:
	// Status: VA
	// Data: hello
}

// Example_getRequest demonstrates creating a get request with flags.
func Example_getRequest() {
	// Get with value, CAS, and TTL
	req := meta.NewRequest(meta.CmdGet, "mykey", nil,
		meta.Flag{Type: meta.FlagReturnValue},
		meta.Flag{Type: meta.FlagReturnCAS},
		meta.Flag{Type: meta.FlagReturnTTL},
	)

	var buf bytes.Buffer
	meta.WriteRequest(&buf, req)

	fmt.Printf("%q", buf.String())
	// Output: "mg mykey v c t\r\n"
}

// Example_setRequest demonstrates creating a set request.
func Example_setRequest() {
	// Set with 60-second TTL
	req := meta.NewRequest(meta.CmdSet, "mykey", []byte("hello"),
		meta.Flag{Type: meta.FlagTTL, Token: "60"},
	)

	var buf bytes.Buffer
	meta.WriteRequest(&buf, req)

	fmt.Printf("%q", buf.String())
	// Output: "ms mykey 5 T60\r\nhello\r\n"
}

// Example_arithmeticRequest demonstrates incrementing a counter.
func Example_arithmeticRequest() {
	// Increment by 5, return value
	req := meta.NewRequest(meta.CmdArithmetic, "counter", nil,
		meta.Flag{Type: meta.FlagReturnValue},
		meta.Flag{Type: meta.FlagDelta, Token: "5"},
	)

	var buf bytes.Buffer
	meta.WriteRequest(&buf, req)

	fmt.Printf("%q", buf.String())
	// Output: "ma counter v D5\r\n"
}

// ExampleWriteRequest_pipelining demonstrates pipelining multiple requests.
func ExampleWriteRequest_pipelining() {
	reqs := []*meta.Request{
		meta.NewRequest(meta.CmdGet, "key1", nil, meta.Flag{Type: meta.FlagReturnValue}, meta.Flag{Type: meta.FlagQuiet}),
		meta.NewRequest(meta.CmdGet, "key2", nil, meta.Flag{Type: meta.FlagReturnValue}, meta.Flag{Type: meta.FlagQuiet}),
		meta.NewRequest(meta.CmdGet, "key3", nil, meta.Flag{Type: meta.FlagReturnValue}),
		meta.NewRequest(meta.CmdNoOp, "", nil),
	}

	var buf bytes.Buffer
	for _, req := range reqs {
		_, err := meta.WriteRequest(&buf, req)
		if err != nil {
			log.Fatal(err)
		}
	}

	fmt.Printf("%q", buf.String())
	// Output: "mg key1 v q\r\nmg key2 v q\r\nmg key3 v\r\nmn\r\n"
}

// ExampleResponse_GetFlagToken demonstrates extracting flag values.
func ExampleResponse_GetFlagToken() {
	input := "HD c12345 t3600\r\n"
	r := bufio.NewReader(bytes.NewBufferString(input))

	resp, err := meta.ReadResponse(r)
	if err != nil {
		log.Fatal(err)
	}

	casValue := resp.GetFlagToken(meta.FlagReturnCAS)
	ttl := resp.GetFlagToken(meta.FlagReturnTTL)

	fmt.Printf("CAS: %s\n", casValue)
	fmt.Printf("TTL: %s\n", ttl)
	// Output:
	// CAS: 12345
	// TTL: 3600
}

// Example_casOperation demonstrates compare-and-swap operations.
func Example_casOperation() {
	// Get request returning CAS token
	getReq := meta.NewRequest(meta.CmdGet, "mykey", nil,
		meta.Flag{Type: meta.FlagReturnCAS},
	)

	var buf bytes.Buffer
	meta.WriteRequest(&buf, getReq)
	fmt.Printf("Get: %q\n", buf.String())

	// Set request with CAS check
	buf.Reset()
	setReq := meta.NewRequest(meta.CmdSet, "mykey", []byte("new value"),
		meta.Flag{Type: meta.FlagCAS, Token: "12345"},
	)

	meta.WriteRequest(&buf, setReq)
	fmt.Printf("Set: %q\n", buf.String())
	// Output:
	// Get: "mg mykey c\r\n"
	// Set: "ms mykey 9 C12345\r\nnew value\r\n"
}

// ExampleShouldCloseConnection demonstrates error handling with connection state.
func ExampleShouldCloseConnection() {
	// Simulate CLIENT_ERROR response
	input := "CLIENT_ERROR bad command line format\r\n"
	r := bufio.NewReader(bytes.NewBufferString(input))

	resp, err := meta.ReadResponse(r)
	if err != nil {
		log.Fatal(err)
	}

	if resp.HasError() {
		if meta.ShouldCloseConnection(resp.Error) {
			fmt.Println("Must close connection")
		} else {
			fmt.Println("Can retry on same connection")
		}
	}
	// Output: Must close connection
}

// ExampleResponse_HasWinFlag demonstrates stale-while-revalidate pattern.
func ExampleResponse_HasWinFlag() {
	// Simulate stale value with win flag
	input := "VA 5 X W\r\nhello\r\n"
	r := bufio.NewReader(bytes.NewBufferString(input))

	resp, err := meta.ReadResponse(r)
	if err != nil {
		log.Fatal(err)
	}

	if resp.HasWinFlag() {
		fmt.Println("Won the race to recache")
	}

	if resp.HasStaleFlag() {
		fmt.Println("Value is stale")
	}

	// Output:
	// Won the race to recache
	// Value is stale
}
