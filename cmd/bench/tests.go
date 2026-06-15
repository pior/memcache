package main

import (
	"context"
	"fmt"
	"time"

	"github.com/pior/memcache"
)

// benchmarkTests returns the ordered operation suite. The order matters: some
// operations read or delete keys written by earlier ones (get-hit after set,
// delete-found after set), so they must run in sequence within a single run.
func benchmarkTests() []Test {
	data10kb := make([]byte, 1024*10)

	return []Test{
		{
			Name:       "get-miss",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name:       "set",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Set(ctx, memcache.Item{
					Key:   key,
					Value: []byte("benchmark-value-0123456789"),
					TTL:   memcache.ExpiresIn(time.Minute),
				})
			},
		},
		{
			Name:       "multi-set-10",
			ItemsPerOp: 10,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				items := make([]memcache.Item, 10)
				for i := range 10 {
					items[i] = memcache.Item{
						Key:   fmt.Sprintf("test-%d-%d-%d-%d", uid, workerID, operationID, i),
						Value: []byte("benchmark-value-0123456789"),
						TTL:   memcache.ExpiresIn(time.Minute),
					}
				}
				return batchCmd.MultiSet(ctx, items)
			},
		},
		{
			Name:       "get-hit",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name:       "multi-get-hit-10",
			ItemsPerOp: 10,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				keys := make([]string, 10)
				for i := range 10 {
					keys[i] = fmt.Sprintf("test-%d-%d-%d-%d", uid, workerID, operationID, i)
				}
				_, err := batchCmd.MultiGet(ctx, keys)
				return err
			},
		},
		{
			Name:       "set-10kb",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Set(ctx, memcache.Item{
					Key:   key,
					Value: data10kb,
					TTL:   memcache.ExpiresIn(time.Minute),
				})
			},
		},
		{
			Name:       "get-hit-10kb",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name:       "delete-found",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Delete(ctx, key)
			},
		},
		{
			Name:       "delete-miss",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Delete(ctx, key)
			},
		},
		{
			Name:       "increment",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-counter", uid)
				_, err := client.Increment(ctx, key, 1, memcache.ExpiresIn(time.Minute))
				return err
			},
		},
	}
}
