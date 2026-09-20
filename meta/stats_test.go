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

// wrapsType reports whether err is, or wraps, an error of type E. It gives a
// test table a plain predicate per row instead of a type switch over As targets.
func wrapsType[E error](err error) bool {
	_, ok := errors.AsType[E](err)
	return ok
}

func TestReadStatsResponse_Errors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType string
		isType   func(error) bool
	}{
		{"CLIENT_ERROR", "CLIENT_ERROR bad command\r\n", "*ClientError", wrapsType[*ClientError]},
		{"SERVER_ERROR", "SERVER_ERROR busy\r\n", "*ServerError", wrapsType[*ServerError]},
		{"ERROR", "ERROR\r\n", "*GenericError", wrapsType[*GenericError]},
		{"garbage line", "GARBAGE LINE\r\nEND\r\n", "*ParseError", wrapsType[*ParseError]},
		{"STAT without value", "STAT lonely\r\nEND\r\n", "*ParseError", wrapsType[*ParseError]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readStats(t, tt.input)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !tt.isType(err) {
				t.Errorf("error = %v (%T), want %s", err, err, tt.wantType)
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

	// A stats stream is a sequence of unbounded-length lines; the same
	// line-length bound must protect it from a server that never sends '\n'.
	t.Run("over-long line returns ParseError with collected stats", func(t *testing.T) {
		const bufSize = MaxLineSize
		input := "STAT pid 1\r\nSTAT huge " + strings.Repeat("v", 4*bufSize) + "\r\nEND\r\n"

		stats, err := ReadStatsResponse(bufio.NewReaderSize(strings.NewReader(input), bufSize))
		if _, ok := errors.AsType[*ParseError](err); !ok {
			t.Fatalf("error = %v (%T), want *ParseError", err, err)
		}
		if got := stats["pid"]; got != "1" {
			t.Errorf("stats collected before the over-long line must be returned, got %v", stats)
		}
	})
}
