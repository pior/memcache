package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *Response
		wantErr error
	}{
		{
			name:  "HD response",
			input: "HD\r\n",
			want: &Response{
				Status: "HD",
			},
		},
		{
			name:  "VA response with value",
			input: "VA 5\r\nhello\r\n",
			want: &Response{
				Status: "VA",
				Value:  []byte("hello"),
			},
		},
		{
			name:  "VA response with value and size flag",
			input: "VA 11 s11\r\nhello world\r\n",
			want: &Response{
				Status: "VA",
				Flags:  Flags{{Type: "s", Value: "11"}},
				Value:  []byte("hello world"),
			},
		},
		{
			name:    "VA response too long",
			input:   "VA 2000000 \r\n....\r\n",
			want:    nil,
			wantErr: errors.Join(ErrProtocolError, errors.New("value size out of bounds")),
		},
		{
			name:  "response with opaque",
			input: "HD O123\r\n",
			want: &Response{
				Status: "HD",
				Flags: Flags{
					{Type: "O", Value: "123"},
				},
				Opaque: "123",
			},
		},
		{
			name:  "response with flags",
			input: "HD f30 c456\r\n",
			want: &Response{
				Status: StatusHD,
				Flags: Flags{
					{Type: "f", Value: "30"},
					{Type: "c", Value: "456"},
				},
			},
		},
		{
			name:    "empty response",
			input:   "\r\n",
			wantErr: ErrInvalidResponse,
		},
		{
			name:    "invalid status",
			input:   "XX\r\n",
			want:    nil,
			wantErr: ErrInvalidResponse,
		},
		{
			name:  "error status",
			input: "ERROR\r\n",
			want: &Response{
				Status: StatusError,
				Error:  fmt.Errorf("memcache: error"),
			},
		},
		{
			name:  "server_error status",
			input: "SERVER_ERROR something went wrong\r\n",
			want: &Response{
				Status: StatusServerError,
				Value:  []byte("SERVER_ERROR something went wrong"),
				Error:  fmt.Errorf("memcache: server error"),
			},
		},
		{
			name:  "client_error status",
			input: "CLIENT_ERROR something went wrong\r\n",
			want: &Response{
				Status: StatusClientError,
				Value:  []byte("CLIENT_ERROR something went wrong"),
				Error:  fmt.Errorf("memcache: client error"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tt.input))
			result, err := ReadResponse(reader)

			require.Equal(t, tt.wantErr, err)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestParseResponseSequence(t *testing.T) {
	responses := "HD O1\r\nHD O2\r\nVA 5 O3\r\nhello\r\n"
	reader := bufio.NewReader(strings.NewReader(responses))

	// First response
	resp1, err := ReadResponse(reader)
	require.NoError(t, err)
	require.Equal(t, StatusHD, resp1.Status)
	require.Equal(t, "1", resp1.Opaque)

	// Second response
	resp2, err := ReadResponse(reader)
	require.NoError(t, err)
	require.Equal(t, StatusHD, resp2.Status)
	require.Equal(t, "2", resp2.Opaque)

	// Third response
	resp3, err := ReadResponse(reader)
	require.NoError(t, err)
	require.Equal(t, StatusVA, resp3.Status)
	require.Equal(t, "3", resp3.Opaque)
	require.Equal(t, []byte("hello"), resp3.Value)
}

func FuzzReadResponse(f *testing.F) {
	// fuzz: elapsed: 21m8s, execs: 195091866 (167173/sec), new interesting: 56 (total: 95)

	// Seed corpus with various response formats
	f.Add("HD\r\n")
	f.Add("HD O123\r\n")
	f.Add("VA 0 s0\r\n\r\n")
	f.Add("VA 5 s5 OABCDEF\r\nhello\r\n")
	f.Add("VA 2000000 s5 OABCDEF\r\n")
	f.Add("EN\r\n")
	f.Add("NS\r\n")
	f.Add("EX\r\n")
	f.Add("NF\r\n")
	f.Add("MN\r\n")
	f.Add("ME thekey key1=value1 key2=value2\r\n")
	f.Add("ERROR\r\n")
	f.Add("SERVER_ERROR something went wrong\r\n")
	f.Add("CLIENT_ERROR something went wrong\r\n")

	f.Fuzz(func(t *testing.T, input string) {
		reader := bufio.NewReader(strings.NewReader(input))

		resp, err := ReadResponse(reader)
		if err == nil {
			require.NotNil(t, resp)
			return
		}

		if errors.Is(err, ErrInvalidResponse) {
			return // Expected error for invalid responses
		}

		if errors.Is(err, ErrProtocolError) {
			return // Expected error for read errors
		}

		require.FailNow(t, "unexpected error", "error is %q", err)
	})
}
