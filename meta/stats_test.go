package meta

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func readStats(t *testing.T, input string) (map[string]string, error) {
	t.Helper()
	return ReadStatsResponse(bufio.NewReader(strings.NewReader(input)))
}

func TestReadStatsResponse(t *testing.T) {
	t.Run("typical response", func(t *testing.T) {
		stats, err := readStats(t, "STAT pid 12345\r\nSTAT uptime 3600\r\nSTAT version 1.6.39\r\nEND\r\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := stats["pid"]; got != "12345" {
			t.Errorf("stats[pid] = %q, want %q", got, "12345")
		}
		if got := stats["version"]; got != "1.6.39" {
			t.Errorf("stats[version] = %q, want %q", got, "1.6.39")
		}
		if len(stats) != 3 {
			t.Errorf("len(stats) = %d, want 3", len(stats))
		}
	})

	t.Run("empty response", func(t *testing.T) {
		stats, err := readStats(t, "END\r\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stats) != 0 {
			t.Errorf("len(stats) = %d, want 0", len(stats))
		}
	})

	t.Run("value containing spaces", func(t *testing.T) {
		stats, err := readStats(t, "STAT slab_global_page_pool 0 0\r\nEND\r\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := stats["slab_global_page_pool"]; got != "0 0" {
			t.Errorf("value = %q, want %q", got, "0 0")
		}
	})

	t.Run("LF only line endings are tolerated", func(t *testing.T) {
		stats, err := readStats(t, "STAT pid 1\nEND\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := stats["pid"]; got != "1" {
			t.Errorf("stats[pid] = %q, want %q", got, "1")
		}
	})
}

func TestReadStatsResponse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr any // pointer to the expected error type
	}{
		{name: "CLIENT_ERROR", input: "CLIENT_ERROR bad command\r\n", wantErr: new(*ClientError)},
		{name: "SERVER_ERROR", input: "SERVER_ERROR busy\r\n", wantErr: new(*ServerError)},
		{name: "ERROR", input: "ERROR\r\n", wantErr: new(*GenericError)},
		{name: "garbage line", input: "GARBAGE LINE\r\nEND\r\n", wantErr: new(*ParseError)},
		{name: "STAT without value", input: "STAT lonely\r\nEND\r\n", wantErr: new(*ParseError)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readStats(t, tt.input)
			if err == nil {
				t.Fatal("expected an error")
			}
			switch want := tt.wantErr.(type) {
			case **ClientError:
				if !errors.As(err, want) {
					t.Errorf("error = %v (%T), want ClientError", err, err)
				}
			case **ServerError:
				if !errors.As(err, want) {
					t.Errorf("error = %v (%T), want ServerError", err, err)
				}
			case **GenericError:
				if !errors.As(err, want) {
					t.Errorf("error = %v (%T), want GenericError", err, err)
				}
			case **ParseError:
				if !errors.As(err, want) {
					t.Errorf("error = %v (%T), want ParseError", err, err)
				}
			}
		})
	}

	t.Run("EOF before END returns collected stats and the error", func(t *testing.T) {
		stats, err := readStats(t, "STAT pid 1\r\n")
		if !errors.Is(err, io.EOF) {
			t.Fatalf("error = %v, want io.EOF", err)
		}
		if got := stats["pid"]; got != "1" {
			t.Errorf("stats collected before EOF must be returned, got %v", stats)
		}
	})
}
