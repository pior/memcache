package memcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
)

var ctx = context.Background()

// newBenchmarkClient creates a test client with a cycling mock connection for benchmarks
func newBenchmarkClient(b *testing.B, newPool PoolFunc, responseData ...string) *Client {
	mockConn := testutils.NewConnectionMock(responseData...)
	mockConn.EnableCycling()
	return newTestClient(b, mockConn, func(c *Config) {
		c.NewPool = newPool
	})
}

func forAllPools(b *testing.B, fn func(b *testing.B, newPool PoolFunc)) {
	b.Run("PuddlePool", func(b *testing.B) {
		fn(b, NewPuddlePool)
	})
	b.Run("ChannelPool", func(b *testing.B) {
		fn(b, NewChannelPool)
	})
}

// BenchmarkClient/Get-8         	 					 3585302	     320.6 ns/op	     269 B/op	       7 allocs/op
// BenchmarkClient/Get_Miss-8    	 					 4237674	     287.0 ns/op	     239 B/op	       5 allocs/op
// BenchmarkClient/Set-8         	 					 4347976	     279.0 ns/op	     245 B/op	       4 allocs/op
// BenchmarkClient/Set_WithTTL-8 	 					 3969376	     308.9 ns/op	     275 B/op	       5 allocs/op
// BenchmarkClient/Set_LargeValue-8         	 		  175866	     12618 ns/op	   36824 B/op	       5 allocs/op
// BenchmarkClient/Add-8                    	 		 3685261	     316.0 ns/op	     280 B/op	       5 allocs/op
// BenchmarkClient/Delete-8                 	 		 4579128	     246.4 ns/op	     213 B/op	       4 allocs/op
// BenchmarkClient/Increment-8              	 		 3220764	     361.1 ns/op	     387 B/op	       7 allocs/op
// BenchmarkClient/Increment_WithTTL-8      	 		 2898552	     414.9 ns/op	     588 B/op	       8 allocs/op
// BenchmarkClient/Increment_NegativeDelta-8         	 3168056	     375.2 ns/op	     420 B/op	       7 allocs/op
// BenchmarkClient/MixedOperations-8                 	 4063699	     293.9 ns/op	     259 B/op	       5 allocs/op
// BenchmarkClient/MultiGet_5keys-8                  	  287566	      4195 ns/op	    2481 B/op	      56 allocs/op
// BenchmarkClient/MultiGet_10keys-8                 	  212500	      6201 ns/op	    4301 B/op	      93 allocs/op
// BenchmarkClient/MultiGet_50keys-8                 	   69439	     17113 ns/op	   19819 B/op	     377 allocs/op
// BenchmarkClient/MultiGet_MixedHitsMisses-8        	  293364	      4028 ns/op	    2429 B/op	      52 allocs/op
// BenchmarkClient/MultiSet_5items-8                 	  318237	      3888 ns/op	    1931 B/op	      40 allocs/op
// BenchmarkClient/MultiSet_10items-8                	  201732	      5133 ns/op	    3507 B/op	      62 allocs/op
// BenchmarkClient/MultiSet_50items-8                	   68754	     14861 ns/op	   15825 B/op	     226 allocs/op
// BenchmarkClient/MultiSet_WithTTL-8                	  296904	      3944 ns/op	    2292 B/op	      45 allocs/op
// BenchmarkClient/MultiDelete_5keys-8               	  329581	      3555 ns/op	    1822 B/op	      40 allocs/op
// BenchmarkClient/MultiDelete_10keys-8              	  250324	      4683 ns/op	    3209 B/op	      62 allocs/op
// BenchmarkClient/MultiDelete_50keys-8              	   87312	     13759 ns/op	   14434 B/op	     226 allocs/op
// BenchmarkClient/MultiDelete_MixedFoundNotFound-8  	  334750	      3547 ns/op	    1820 B/op	      40 allocs/op
func BenchmarkClient(b *testing.B) {
	forAllPools(b, func(b *testing.B, newPool PoolFunc) {
		b.Run("Get", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool,
				"VA 5\r\n",
				"hello\r\n",
			)

			for b.Loop() {
				if _, err := client.Get(ctx, "testkey"); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Get_Miss", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "EN\r\n")

			for b.Loop() {
				if _, err := client.Get(ctx, "testkey"); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Set", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "HD\r\n")
			item := Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   NoTTL,
			}

			for b.Loop() {
				if err := client.Set(ctx, item); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Set_WithTTL", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "HD\r\n")
			item := Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   60 * time.Second,
			}

			for b.Loop() {
				if err := client.Set(ctx, item); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Set_LargeValue", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "HD\r\n")
			largeValue := make([]byte, 10240)
			item := Item{
				Key:   "key",
				Value: largeValue,
				TTL:   NoTTL,
			}

			for b.Loop() {
				if err := client.Set(ctx, item); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Add", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "HD\r\n")
			item := Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   NoTTL,
			}

			for b.Loop() {
				if err := client.Add(ctx, item); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Delete", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "HD\r\n")

			for b.Loop() {
				if err := client.Delete(ctx, "key"); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Increment", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "VA 1\r\n5\r\n")

			for b.Loop() {
				if _, err := client.Increment(ctx, "counter", 1, NoTTL); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Increment_WithTTL", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "VA 1\r\n5\r\n")

			for b.Loop() {
				if _, err := client.Increment(ctx, "counter", 1, 60*time.Second); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Increment_NegativeDelta", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool, "VA 1\r\n0\r\n")

			for b.Loop() {
				if _, err := client.Increment(ctx, "counter", -1, NoTTL); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MixedOperations", func(b *testing.B) {
			client := newBenchmarkClient(b, newPool,
				"HD\r\n",
				"VA 5\r\nhello\r\n",
				"HD\r\n",
				"VA 1\r\n5\r\n",
			)
			item := Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   NoTTL,
			}

			for i := range b.N {
				var err error
				switch i % 4 {
				case 0:
					err = client.Set(ctx, item)
				case 1:
					_, err = client.Get(ctx, "key")
				case 2:
					err = client.Delete(ctx, "key")
				case 3:
					_, err = client.Increment(ctx, "counter", 1, NoTTL)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})

		// Batch operation benchmarks
		b.Run("MultiGet_5keys", func(b *testing.B) {
			// Mock response: 5 successful gets
			client := newBenchmarkClient(b, newPool,
				"VA 5\r\nhello\r\n",
				"VA 5\r\nworld\r\n",
				"VA 3\r\nfoo\r\n",
				"VA 3\r\nbar\r\n",
				"VA 4\r\ntest\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			keys := []string{"key1", "key2", "key3", "key4", "key5"}

			for b.Loop() {
				if _, err := batchCmd.MultiGet(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiGet_10keys", func(b *testing.B) {
			// Mock response: 10 successful gets
			client := newBenchmarkClient(b, newPool,
				"VA 5\r\nhello\r\n",
				"VA 5\r\nworld\r\n",
				"VA 3\r\nfoo\r\n",
				"VA 3\r\nbar\r\n",
				"VA 4\r\ntest\r\n",
				"VA 5\r\nhello\r\n",
				"VA 5\r\nworld\r\n",
				"VA 3\r\nfoo\r\n",
				"VA 3\r\nbar\r\n",
				"VA 4\r\ntest\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			keys := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9", "k10"}

			for b.Loop() {
				if _, err := batchCmd.MultiGet(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiGet_50keys", func(b *testing.B) {
			// Mock response: 50 successful gets (smaller values for efficiency)
			var mockResp string
			for range 50 {
				mockResp += "VA 1\r\nx\r\n"
			}
			mockResp += "MN\r\n"
			client := newBenchmarkClient(b, newPool, mockResp)
			batchCmd := NewBatchCommands(client)
			keys := make([]string, 50)
			for i := 0; i < 50; i++ {
				keys[i] = fmt.Sprintf("key%d", i)
			}

			for b.Loop() {
				if _, err := batchCmd.MultiGet(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiGet_MixedHitsMisses", func(b *testing.B) {
			// Mock response: mix of hits and misses (5 keys: hit, miss, hit, miss, hit)
			client := newBenchmarkClient(b, newPool,
				"VA 5\r\nhello\r\n",
				"EN\r\n",
				"VA 5\r\nworld\r\n",
				"EN\r\n",
				"VA 4\r\ntest\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			keys := []string{"key1", "key2", "key3", "key4", "key5"}

			for b.Loop() {
				if _, err := batchCmd.MultiGet(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiSet_5items", func(b *testing.B) {
			// Mock response: 5 successful sets
			client := newBenchmarkClient(b, newPool,
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			items := []Item{
				{Key: "key1", Value: []byte("value1")},
				{Key: "key2", Value: []byte("value2")},
				{Key: "key3", Value: []byte("value3")},
				{Key: "key4", Value: []byte("value4")},
				{Key: "key5", Value: []byte("value5")},
			}

			for b.Loop() {
				if err := batchCmd.MultiSet(ctx, items); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiSet_10items", func(b *testing.B) {
			// Mock response: 10 successful sets
			var mockResp string
			for range 10 {
				mockResp += "HD\r\n"
			}
			mockResp += "MN\r\n"
			client := newBenchmarkClient(b, newPool, mockResp)
			batchCmd := NewBatchCommands(client)
			items := make([]Item, 10)
			for i := 0; i < 10; i++ {
				items[i] = Item{Key: fmt.Sprintf("key%d", i), Value: []byte("value")}
			}

			for b.Loop() {
				if err := batchCmd.MultiSet(ctx, items); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiSet_50items", func(b *testing.B) {
			// Mock response: 50 successful sets
			var mockResp string
			for range 50 {
				mockResp += "HD\r\n"
			}
			mockResp += "MN\r\n"
			client := newBenchmarkClient(b, newPool, mockResp)
			batchCmd := NewBatchCommands(client)
			items := make([]Item, 50)
			for i := 0; i < 50; i++ {
				items[i] = Item{Key: fmt.Sprintf("key%d", i), Value: []byte("x")}
			}

			for b.Loop() {
				if err := batchCmd.MultiSet(ctx, items); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiSet_WithTTL", func(b *testing.B) {
			// Mock response: 5 successful sets with TTL
			client := newBenchmarkClient(b, newPool,
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			items := []Item{
				{Key: "key1", Value: []byte("value1"), TTL: 60 * time.Second},
				{Key: "key2", Value: []byte("value2"), TTL: 60 * time.Second},
				{Key: "key3", Value: []byte("value3"), TTL: 60 * time.Second},
				{Key: "key4", Value: []byte("value4"), TTL: 60 * time.Second},
				{Key: "key5", Value: []byte("value5"), TTL: 60 * time.Second},
			}

			for b.Loop() {
				if err := batchCmd.MultiSet(ctx, items); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiDelete_5keys", func(b *testing.B) {
			// Mock response: 5 successful deletes
			client := newBenchmarkClient(b, newPool,
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"HD\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			keys := []string{"key1", "key2", "key3", "key4", "key5"}

			for b.Loop() {
				if err := batchCmd.MultiDelete(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiDelete_10keys", func(b *testing.B) {
			// Mock response: 10 successful deletes
			var mockResp string
			for range 10 {
				mockResp += "HD\r\n"
			}
			mockResp += "MN\r\n"
			client := newBenchmarkClient(b, newPool, mockResp)
			batchCmd := NewBatchCommands(client)
			keys := make([]string, 10)
			for i := 0; i < 10; i++ {
				keys[i] = fmt.Sprintf("key%d", i)
			}

			for b.Loop() {
				if err := batchCmd.MultiDelete(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiDelete_50keys", func(b *testing.B) {
			// Mock response: 50 successful deletes
			var mockResp string
			for i := 0; i < 50; i++ {
				mockResp += "HD\r\n"
			}
			mockResp += "MN\r\n"
			client := newBenchmarkClient(b, newPool, mockResp)
			batchCmd := NewBatchCommands(client)
			keys := make([]string, 50)
			for i := 0; i < 50; i++ {
				keys[i] = fmt.Sprintf("key%d", i)
			}

			for b.Loop() {
				if err := batchCmd.MultiDelete(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("MultiDelete_MixedFoundNotFound", func(b *testing.B) {
			// Mock response: mix of found and not found (5 keys: found, not found, found, not found, found)
			client := newBenchmarkClient(b, newPool,
				"HD\r\n",
				"NF\r\n",
				"HD\r\n",
				"NF\r\n",
				"HD\r\n",
				"MN\r\n",
			)
			batchCmd := NewBatchCommands(client)
			keys := []string{"key1", "key2", "key3", "key4", "key5"}

			for b.Loop() {
				if err := batchCmd.MultiDelete(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}
