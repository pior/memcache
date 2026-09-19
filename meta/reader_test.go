package meta

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

var allStatuses = []StatusType{StatusHD, StatusVA, StatusEN, StatusNF, StatusNS, StatusEX, StatusMN, StatusME, StatusOK}

// acceptedStatuses returns the statuses ValidateResponse accepts for req, as a
// space-separated string, so a wrong table shows up as a readable diff.
func acceptedStatuses(req *Request, respErr error) string {
	var accepted []string
	for _, status := range allStatuses {
		if ValidateResponse(req, &Response{Status: status, Error: respErr}) == nil {
			accepted = append(accepted, string(status))
		}
	}
	return strings.Join(accepted, " ")
}

func TestValidateResponse(t *testing.T) {
	const all = "HD VA EN NF NS EX MN ME OK"

	tests := []struct {
		name string
		req  *Request
		want string
	}{
		{"mg with v", NewRequest(CmdGet, "k", nil).AddReturnValue(), "VA EN"},
		{"mg with v among other flags", NewRequest(CmdGet, "k", nil).AddReturnCAS().AddReturnValue(), "VA EN"},
		{"mg without v", NewRequest(CmdGet, "k", nil).AddReturnCAS(), "HD EN"},
		{"mg quiet with v", NewRequest(CmdGet, "k", nil).AddReturnValue().AddQuiet(), "VA EN"},
		{"ms", NewRequest(CmdSet, "k", []byte("v")), "HD NF NS EX"},
		{"md", NewRequest(CmdDelete, "k", nil), "HD NF NS EX"},
		{"ma with v", NewRequest(CmdArithmetic, "k", nil).AddReturnValue(), "VA NF NS EX"},
		{"ma without v", NewRequest(CmdArithmetic, "k", nil), "HD NF NS EX"},
		{"me", NewRequest(CmdDebug, "k", nil), "EN ME"},
		{"mn", NewRequest(CmdNoOp, "", nil), "MN"},
		{"flush_all", NewRequest(CmdFlushAll, "", nil), "OK"},
		{"custom command", NewRequest(CmdType("mx"), "k", nil), all},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acceptedStatuses(tt.req, nil); got != tt.want {
				t.Errorf("accepted statuses = %q, want %q", got, tt.want)
			}
		})

		t.Run(tt.name+" with protocol error", func(t *testing.T) {
			// ERROR, CLIENT_ERROR and SERVER_ERROR can follow any command.
			if got := acceptedStatuses(tt.req, &ServerError{Message: "out of memory"}); got != all {
				t.Errorf("accepted statuses = %q, want %q", got, all)
			}
		})
	}

	t.Run("rejection closes the connection", func(t *testing.T) {
		err := ValidateResponse(NewRequest(CmdGet, "k", nil).AddReturnValue(), &Response{Status: StatusHD})

		var parseErr *ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("error = %v (%T), want *ParseError", err, err)
		}
		if !ShouldCloseConnection(err) {
			t.Error("ShouldCloseConnection = false, want true")
		}
		if got, want := err.Error(), "parse error: unexpected HD reply to mg"; got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})
}

// TestReadResponseBoundsDataAllocation pins the memory a single response can
// commit. A VA header declares a length the client has not received yet, so
// allocating that length up front lets a hostile or buggy server turn a
// 15-byte reply into a MaxDataSize (1 GiB) allocation, once per connection.
//
// What matters is that the allocation is bounded by a constant rather than by
// the declared size, so each case declares a size far above the bound and
// checks the buffer did not follow it.
func TestReadResponseBoundsDataAllocation(t *testing.T) {
	// slices.Grow rounds up to a size class, so the bound is the cap plus
	// headroom rather than an exact figure.
	const wantCap = 2 * maxDataPrealloc

	tests := map[string]struct {
		input    string
		declared int
		wantData string
	}{
		"declared 1 GiB, nothing sent":   {"VA 1073741824\r\n", 1 << 30, ""},
		"declared 64 MiB, nothing sent":  {"VA 67108864\r\n", 64 << 20, ""},
		"declared 64 MiB, 8 bytes sent":  {"VA 67108864\r\n12345678", 64 << 20, "12345678"},
		"declared 16 MiB, partial value": {"VA 16777216\r\nabc", 16 << 20, "abc"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := bufio.NewReaderSize(strings.NewReader(tt.input), MaxLineSize)

			var resp Response
			err := ReadResponse(r, &resp)
			if err == nil {
				t.Fatal("expected a parse error for a truncated data block")
			}
			if cap(resp.Data) > wantCap {
				t.Errorf("allocated %d bytes for %d bytes of payload, want at most %d",
					cap(resp.Data), len(tt.wantData), wantCap)
			}
			if cap(resp.Data) >= tt.declared {
				t.Errorf("allocation %d tracked the declared size %d", cap(resp.Data), tt.declared)
			}
			if string(resp.Data) != tt.wantData {
				t.Errorf("data = %q, want %q", resp.Data, tt.wantData)
			}
		})
	}
}

// TestReadResponseLargeValue checks the bounded read still returns a value
// larger than the initial allocation intact.
func TestReadResponseLargeValue(t *testing.T) {
	for _, size := range []int{0, 1, 1024, 64 << 10, maxDataPrealloc - 2, maxDataPrealloc, maxDataPrealloc + 1, 3 * maxDataPrealloc} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			value := bytes.Repeat([]byte("x"), size)
			input := fmt.Sprintf("VA %d\r\n%s\r\n", size, value)
			r := bufio.NewReaderSize(strings.NewReader(input), MaxLineSize)

			var resp Response
			if err := ReadResponse(r, &resp); err != nil {
				t.Fatalf("ReadResponse: %v", err)
			}
			if len(resp.Data) != size {
				t.Fatalf("read %d bytes, want %d", len(resp.Data), size)
			}
			if !bytes.Equal(resp.Data, value) {
				t.Error("value does not round-trip")
			}
		})
	}
}

// TestReadResponseTruncatedDataBlockError pins which error a truncated value
// produces. io.EOF means the server closed between responses; ErrUnexpectedEOF
// means it closed part-way through a value. Because the data buffer is reused
// across responses, the chunking of the read must not decide which one the
// caller sees.
func TestReadResponseTruncatedDataBlockError(t *testing.T) {
	tests := map[string]struct {
		stream  string
		wantErr error
	}{
		"no data at all":            {"VA 4\r\n", io.EOF},
		"partial data":              {"VA 4\r\nab", io.ErrUnexpectedEOF},
		"data without terminator":   {"VA 4\r\nabcd", io.ErrUnexpectedEOF},
		"after a previous response": {"VA 2\r\nxy\r\nVA 9\r\n", io.EOF},
		"partial after previous":    {"VA 2\r\nxy\r\nVA 9\r\n01234567", io.ErrUnexpectedEOF},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := bufio.NewReaderSize(strings.NewReader(tt.stream), MaxLineSize)

			var resp Response
			var err error
			for err == nil {
				err = ReadResponse(r, &resp)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want one wrapping %v", err, tt.wantErr)
			}
		})
	}
}
