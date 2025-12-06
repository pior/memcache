package memcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
)

var ctx = context.Background()

// BenchmarkClient/Get-8         						 1364875	     792.0 ns/op	    8618 B/op	       9 allocs/op
// BenchmarkClient/Get_Miss-8   					 	 1399284	     799.6 ns/op	    8617 B/op	       9 allocs/op
// BenchmarkClient/Set-8        					 	 1550454	     730.7 ns/op	    8588 B/op	       8 allocs/op
// BenchmarkClient/Set_WithTTL-8 						 1546633	     747.8 ns/op	    8612 B/op	       9 allocs/op
// BenchmarkClient/Set_LargeValue-8     		    	  148244	      9305 ns/op	   30278 B/op	       9 allocs/op
// BenchmarkClient/Add-8                		    	 1201928	     900.5 ns/op	    8625 B/op	       9 allocs/op
// BenchmarkClient/Delete-8             		    	 1332070	     842.1 ns/op	    8571 B/op	       8 allocs/op
// BenchmarkClient/Increment-8          		    	 1451330	     787.2 ns/op	    8687 B/op	       9 allocs/op
// BenchmarkClient/Increment_WithTTL-8  		    	 1405624	     826.2 ns/op	    8928 B/op	      10 allocs/op
// BenchmarkClient/Increment_NegativeDelta-8         	 1462662	     786.6 ns/op	    8764 B/op	       9 allocs/op
// BenchmarkClient/MixedOperations-8                 	 1409892	     791.5 ns/op	    8623 B/op	       8 allocs/op
// BenchmarkClient/MultiGet_5keys-8                  	  272662	      4060 ns/op	    9757 B/op	      33 allocs/op
// BenchmarkClient/MultiGet_10keys-8                 	  232720	      4763 ns/op	   10690 B/op	      45 allocs/op
// BenchmarkClient/MultiGet_50keys-8                 	  110854	     10880 ns/op	   18657 B/op	     129 allocs/op
// BenchmarkClient/MultiGet_MixedHitsMisses-8        	  273633	      4207 ns/op	    9756 B/op	      33 allocs/op
// BenchmarkClient/MultiSet_5items-8                 	  271093	      4025 ns/op	    9760 B/op	      28 allocs/op
// BenchmarkClient/MultiSet_10items-8                	  230823	      4753 ns/op	   10669 B/op	      35 allocs/op
// BenchmarkClient/MultiSet_50items-8                	  111406	     10604 ns/op	   17448 B/op	      79 allocs/op
// BenchmarkClient/MultiSet_WithTTL-8                	  260398	      4239 ns/op	    9891 B/op	      33 allocs/op
// BenchmarkClient/MultiDelete_5keys-8               	  281050	      3984 ns/op	    9633 B/op	      28 allocs/op
// BenchmarkClient/MultiDelete_10keys-8              	  246657	      4524 ns/op	   10437 B/op	      35 allocs/op
// BenchmarkClient/MultiDelete_50keys-8              	  118573	      9784 ns/op	   16773 B/op	      79 allocs/op
// BenchmarkClient/MultiDelete_MixedFoundNotFound-8  	  282121	      3933 ns/op	    9633 B/op	      28 allocs/op
func BenchmarkClient(b *testing.B) {
	b.Run("Get", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock(
			"VA 5\r\n",
			"hello\r\n",
		)
		client := newTestClient(b, mockConn)

		for b.Loop() {
			_, _ = client.Get(ctx, "testkey")
		}
	})

	b.Run("Get_Miss", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock("EN\r\n")
		client := newTestClient(b, mockConn)

		for b.Loop() {
			_, _ = client.Get(ctx, "testkey")
		}
	})

	b.Run("Set", func(b *testing.B) {
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

	b.Run("Set_WithTTL", func(b *testing.B) {
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

	b.Run("Set_LargeValue", func(b *testing.B) {
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
		mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
		client := newTestClient(b, mockConn)

		for b.Loop() {
			_, _ = client.Increment(ctx, "counter", 1, NoTTL)
		}
	})

	b.Run("Increment_WithTTL", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
		client := newTestClient(b, mockConn)

		for b.Loop() {
			_, _ = client.Increment(ctx, "counter", 1, 60*time.Second)
		}
	})

	b.Run("Increment_NegativeDelta", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
		client := newTestClient(b, mockConn)

		for b.Loop() {
			_, _ = client.Increment(ctx, "counter", -1, NoTTL)
		}
	})

	b.Run("MixedOperations", func(b *testing.B) {
		mockConn := testutils.NewConnectionMock(
			"HD\r\n",
			"VA 5\r\nhello\r\n",
			"HD\r\n",
			"VA 1\r\n5\r\n",
		)
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

	// Batch operation benchmarks
	b.Run("MultiGet_5keys", func(b *testing.B) {
		// Mock response: 5 successful gets
		mockConn := testutils.NewConnectionMock(
			"VA 5\r\nhello\r\n",
			"VA 5\r\nworld\r\n",
			"VA 3\r\nfoo\r\n",
			"VA 3\r\nbar\r\n",
			"VA 4\r\ntest\r\n",
			"MN\r\n",
		)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := []string{"key1", "key2", "key3", "key4", "key5"}

		for b.Loop() {
			_, _ = batchCmd.MultiGet(ctx, keys)
		}
	})

	b.Run("MultiGet_10keys", func(b *testing.B) {
		// Mock response: 10 successful gets
		mockConn := testutils.NewConnectionMock(
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
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9", "k10"}

		for b.Loop() {
			_, _ = batchCmd.MultiGet(ctx, keys)
		}
	})

	b.Run("MultiGet_50keys", func(b *testing.B) {
		// Mock response: 50 successful gets (smaller values for efficiency)
		var mockResp string
		for range 50 {
			mockResp += "VA 1\r\nx\r\n"
		}
		mockResp += "MN\r\n"
		mockConn := testutils.NewConnectionMock(mockResp)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := make([]string, 50)
		for i := 0; i < 50; i++ {
			keys[i] = fmt.Sprintf("key%d", i)
		}

		for b.Loop() {
			_, _ = batchCmd.MultiGet(ctx, keys)
		}
	})

	b.Run("MultiGet_MixedHitsMisses", func(b *testing.B) {
		// Mock response: mix of hits and misses (5 keys: hit, miss, hit, miss, hit)
		mockConn := testutils.NewConnectionMock(
			"VA 5\r\nhello\r\n",
			"EN\r\n",
			"VA 5\r\nworld\r\n",
			"EN\r\n",
			"VA 4\r\ntest\r\n",
			"MN\r\n",
		)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := []string{"key1", "key2", "key3", "key4", "key5"}

		for b.Loop() {
			_, _ = batchCmd.MultiGet(ctx, keys)
		}
	})

	b.Run("MultiSet_5items", func(b *testing.B) {
		// Mock response: 5 successful sets
		mockConn := testutils.NewConnectionMock(
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"MN\r\n",
		)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		items := []Item{
			{Key: "key1", Value: []byte("value1")},
			{Key: "key2", Value: []byte("value2")},
			{Key: "key3", Value: []byte("value3")},
			{Key: "key4", Value: []byte("value4")},
			{Key: "key5", Value: []byte("value5")},
		}

		for b.Loop() {
			_ = batchCmd.MultiSet(ctx, items)
		}
	})

	b.Run("MultiSet_10items", func(b *testing.B) {
		// Mock response: 10 successful sets
		var mockResp string
		for range 10 {
			mockResp += "HD\r\n"
		}
		mockResp += "MN\r\n"
		mockConn := testutils.NewConnectionMock(mockResp)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		items := make([]Item, 10)
		for i := 0; i < 10; i++ {
			items[i] = Item{Key: fmt.Sprintf("key%d", i), Value: []byte("value")}
		}

		for b.Loop() {
			_ = batchCmd.MultiSet(ctx, items)
		}
	})

	b.Run("MultiSet_50items", func(b *testing.B) {
		// Mock response: 50 successful sets
		var mockResp string
		for range 50 {
			mockResp += "HD\r\n"
		}
		mockResp += "MN\r\n"
		mockConn := testutils.NewConnectionMock(mockResp)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		items := make([]Item, 50)
		for i := 0; i < 50; i++ {
			items[i] = Item{Key: fmt.Sprintf("key%d", i), Value: []byte("x")}
		}

		for b.Loop() {
			_ = batchCmd.MultiSet(ctx, items)
		}
	})

	b.Run("MultiSet_WithTTL", func(b *testing.B) {
		// Mock response: 5 successful sets with TTL
		mockConn := testutils.NewConnectionMock(
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"MN\r\n",
		)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		items := []Item{
			{Key: "key1", Value: []byte("value1"), TTL: 60 * time.Second},
			{Key: "key2", Value: []byte("value2"), TTL: 60 * time.Second},
			{Key: "key3", Value: []byte("value3"), TTL: 60 * time.Second},
			{Key: "key4", Value: []byte("value4"), TTL: 60 * time.Second},
			{Key: "key5", Value: []byte("value5"), TTL: 60 * time.Second},
		}

		for b.Loop() {
			_ = batchCmd.MultiSet(ctx, items)
		}
	})

	b.Run("MultiDelete_5keys", func(b *testing.B) {
		// Mock response: 5 successful deletes
		mockConn := testutils.NewConnectionMock(
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"HD\r\n",
			"MN\r\n",
		)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := []string{"key1", "key2", "key3", "key4", "key5"}

		for b.Loop() {
			_ = batchCmd.MultiDelete(ctx, keys)
		}
	})

	b.Run("MultiDelete_10keys", func(b *testing.B) {
		// Mock response: 10 successful deletes
		var mockResp string
		for range 10 {
			mockResp += "HD\r\n"
		}
		mockResp += "MN\r\n"
		mockConn := testutils.NewConnectionMock(mockResp)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := make([]string, 10)
		for i := 0; i < 10; i++ {
			keys[i] = fmt.Sprintf("key%d", i)
		}

		for b.Loop() {
			_ = batchCmd.MultiDelete(ctx, keys)
		}
	})

	b.Run("MultiDelete_50keys", func(b *testing.B) {
		// Mock response: 50 successful deletes
		var mockResp string
		for i := 0; i < 50; i++ {
			mockResp += "HD\r\n"
		}
		mockResp += "MN\r\n"
		mockConn := testutils.NewConnectionMock(mockResp)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := make([]string, 50)
		for i := 0; i < 50; i++ {
			keys[i] = fmt.Sprintf("key%d", i)
		}

		for b.Loop() {
			_ = batchCmd.MultiDelete(ctx, keys)
		}
	})

	b.Run("MultiDelete_MixedFoundNotFound", func(b *testing.B) {
		// Mock response: mix of found and not found (5 keys: found, not found, found, not found, found)
		mockConn := testutils.NewConnectionMock(
			"HD\r\n",
			"NF\r\n",
			"HD\r\n",
			"NF\r\n",
			"HD\r\n",
			"MN\r\n",
		)
		client := newTestClient(b, mockConn)
		batchCmd := NewBatchCommands(client)
		keys := []string{"key1", "key2", "key3", "key4", "key5"}

		for b.Loop() {
			_ = batchCmd.MultiDelete(ctx, keys)
		}
	})
}
