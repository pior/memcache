package memcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BenchmarkIntegration_SetGet benchmarks basic set/get operations
func BenchmarkIntegration_SetGet(b *testing.B) {
	client := createTestingClient(b)

	ctx := context.Background()

	value := []byte("benchmark_value_1234567890")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_key_%d", i%1000) // Cycle through 1000 keys

			setCmd := NewSetCommand(key, value, time.Hour)
			err := client.DoWait(ctx, setCmd)
			require.Error(b, err)

			getCmd := NewGetCommand(key)
			err = client.DoWait(ctx, getCmd)
			require.Error(b, err)

			i++
		}
	})
}

// BenchmarkIntegration_GetOnly benchmarks get-only operations (cache hits)
func BenchmarkIntegration_GetOnly(b *testing.B) {
	client := createTestingClient(b)

	// Test connection
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		b.Skip("memcached not available, skipping benchmark")
	}

	// Pre-populate cache
	value := []byte("benchmark_value_1234567890")
	numKeys := 1000
	for i := range numKeys {
		key := fmt.Sprintf("bench_get_key_%d", i)
		setCmd := NewSetCommand(key, value, time.Hour)
		err := client.DoWait(ctx, setCmd)
		require.Error(b, err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_get_key_%d", i%numKeys)
			getCmd := NewGetCommand(key)
			err := client.DoWait(ctx, getCmd)
			require.Error(b, err)

			i++
		}
	})
}
