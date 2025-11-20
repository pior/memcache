package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
)

var ctx = context.Background()

// BenchmarkClient_Get benchmarks the Get method
func BenchmarkClient_Get(b *testing.B) {
	mockConn := testutils.NewConnectionMock("VA 5\r\nhello\r\n")
	client := newTestClient(b, mockConn)

	for b.Loop() {
		_, _ = client.Get(ctx, "testkey")
	}
}

// BenchmarkClient_Get_Miss benchmarks Get with cache miss
func BenchmarkClient_Get_Miss(b *testing.B) {
	mockConn := testutils.NewConnectionMock("EN\r\n")
	client := newTestClient(b, mockConn)

	for b.Loop() {
		_, _ = client.Get(ctx, "testkey")
	}
}

// BenchmarkClient_Set benchmarks the Set method
func BenchmarkClient_Set(b *testing.B) {
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
}

// BenchmarkClient_Set_WithTTL benchmarks Set with TTL
func BenchmarkClient_Set_WithTTL(b *testing.B) {
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
}

// BenchmarkClient_Set_LargeValue benchmarks Set with 10KB value
func BenchmarkClient_Set_LargeValue(b *testing.B) {
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
}

// BenchmarkClient_Add benchmarks the Add method
func BenchmarkClient_Add(b *testing.B) {
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
}

// BenchmarkClient_Delete benchmarks the Delete method
func BenchmarkClient_Delete(b *testing.B) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(b, mockConn)

	for b.Loop() {
		_ = client.Delete(ctx, "key")
	}
}

// BenchmarkClient_Increment benchmarks the Increment method
func BenchmarkClient_Increment(b *testing.B) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
	client := newTestClient(b, mockConn)

	for b.Loop() {
		_, _ = client.Increment(ctx, "counter", 1, NoTTL)
	}
}

// BenchmarkClient_Increment_WithTTL benchmarks Increment with TTL
func BenchmarkClient_Increment_WithTTL(b *testing.B) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
	client := newTestClient(b, mockConn)

	for b.Loop() {
		_, _ = client.Increment(ctx, "counter", 1, 60*time.Second)
	}
}

// BenchmarkClient_Increment_NegativeDelta benchmarks Increment with negative delta
func BenchmarkClient_Increment_NegativeDelta(b *testing.B) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
	client := newTestClient(b, mockConn)

	for b.Loop() {
		_, _ = client.Increment(ctx, "counter", -1, NoTTL)
	}
}

// BenchmarkClient_MixedOperations benchmarks a mix of operations
func BenchmarkClient_MixedOperations(b *testing.B) {
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
}
