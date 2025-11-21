package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
)

var ctx = context.Background()

func BenchmarkClient(b *testing.B) {
	b.Run("Get", func(b *testing.B) {
		b.Run("Hit", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("VA 5\r\nhello\r\n")
			client := newTestClient(b, mockConn)

			for b.Loop() {
				_, _ = client.Get(ctx, "testkey")
			}
		})

		b.Run("Miss", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("EN\r\n")
			client := newTestClient(b, mockConn)

			for b.Loop() {
				_, _ = client.Get(ctx, "testkey")
			}
		})
	})

	b.Run("Set", func(b *testing.B) {
		b.Run("Standard", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("HD\r\n")
			client := newTestClient(b, mockConn)
			item := Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   NoTTL,
			}

			for b.Loop() {
				_ = client.Set(ctx, item)
			}
		})

		b.Run("WithTTL", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("HD\r\n")
			client := newTestClient(b, mockConn)
			item := Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   60 * time.Second,
			}

			for b.Loop() {
				_ = client.Set(ctx, item)
			}
		})

		b.Run("LargeValue", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("HD\r\n")
			client := newTestClient(b, mockConn)
			largeValue := make([]byte, 10240)
			item := Item{
				Key:   "key",
				Value: largeValue,
				TTL:   NoTTL,
			}

			for b.Loop() {
				_ = client.Set(ctx, item)
			}
		})
	})

	b.Run("Add", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock("HD\r\n")
		client := newTestClient(b, mockConn)
		item := Item{
			Key:   "key",
			Value: []byte("value"),
			TTL:   NoTTL,
		}

		for b.Loop() {
			_ = client.Add(ctx, item)
		}
	})

	b.Run("Delete", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock("HD\r\n")
		client := newTestClient(b, mockConn)

		for b.Loop() {
			_ = client.Delete(ctx, "key")
		}
	})

	b.Run("Increment", func(b *testing.B) {
		b.Run("Positive", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
			client := newTestClient(b, mockConn)

			for b.Loop() {
				_, _ = client.Increment(ctx, "counter", 1, NoTTL)
			}
		})

		b.Run("PositiveWithTTL", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
			client := newTestClient(b, mockConn)

			for b.Loop() {
				_, _ = client.Increment(ctx, "counter", 1, 60*time.Second)
			}
		})

		b.Run("Negative", func(b *testing.B) {
			mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
			client := newTestClient(b, mockConn)

			for b.Loop() {
				_, _ = client.Increment(ctx, "counter", -1, NoTTL)
			}
		})
	})

	b.Run("Mixed", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock("HD\r\nVA 5\r\nhello\r\nHD\r\nVA 1\r\n5\r\n")
		client := newTestClient(b, mockConn)
		item := Item{
			Key:   "key",
			Value: []byte("value"),
			TTL:   NoTTL,
		}

		for i := range b.N {
			switch i % 4 {
			case 0:
				_ = client.Set(ctx, item)
			case 1:
				_, _ = client.Get(ctx, "key")
			case 2:
				_ = client.Delete(ctx, "key")
			case 3:
				_, _ = client.Increment(ctx, "counter", 1, NoTTL)
			}
		}
	})
}
