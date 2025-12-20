package workload

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/pior/memcache"
)

func init() {
	Register(&MixedWorkload{})
	Register(&GetWorkload{})
	Register(&GetHeavyWorkload{})
	Register(&SetHeavyWorkload{})
}

// Config holds workload configuration
type Config struct {
	HotKeyCount atomic.Int32
}

var config Config

func init() {
	// Default: 10 hot keys
	config.HotKeyCount.Store(10)
}

// SetHotKeyCount configures the number of hot keys for workloads
func SetHotKeyCount(count int) {
	config.HotKeyCount.Store(int32(count))
}

// GetHotKeyCount returns the current hot key count
func GetHotKeyCount() int {
	return int(config.HotKeyCount.Load())
}

// MixedWorkload performs a realistic mix of operations
type MixedWorkload struct{}

func (w *MixedWorkload) Name() string {
	return "mixed"
}

func (w *MixedWorkload) Description() string {
	return "Mixed operations: 60% get, 30% set, 5% delete, 5% increment"
}

func (w *MixedWorkload) Execute(ctx context.Context, client *memcache.Client, workerID int) error {
	// Generate a key (skewed distribution to simulate hot keys)
	var key string
	hotKeyCount := GetHotKeyCount()
	if rand.Float64() < 0.3 { // 30% chance of hot key
		key = fmt.Sprintf("hot-key-%d", rand.IntN(hotKeyCount))
	} else {
		key = fmt.Sprintf("key-worker%d-%d", workerID, rand.IntN(1000))
	}

	// Choose operation based on probability
	op := rand.Float64()

	switch {
	case op < 0.60: // 60% GET
		_, err := client.Get(ctx, key)
		return err

	case op < 0.90: // 30% SET
		value := []byte(fmt.Sprintf("value-%d-%d", workerID, time.Now().UnixNano()))
		ttl := time.Duration(30+rand.IntN(60)) * time.Second
		return client.Set(ctx, memcache.Item{
			Key:   key,
			Value: value,
			TTL:   ttl,
		})

	case op < 0.95: // 5% DELETE
		return client.Delete(ctx, key)

	default: // 5% INCREMENT
		counterKey := fmt.Sprintf("counter-worker%d", workerID)
		_, err := client.Increment(ctx, counterKey, 1, memcache.NoTTL)
		return err
	}
}

type GetWorkload struct{}

func (w *GetWorkload) Name() string {
	return "get"
}

func (w *GetWorkload) Description() string {
	return "Read-only workload: 100% get"
}

func (w *GetWorkload) Execute(ctx context.Context, client *memcache.Client, workerID int) error {
	key := fmt.Sprintf("key-%d", rand.IntN(1000))

	_, err := client.Get(ctx, key)
	return err
}

// GetHeavyWorkload is heavily weighted towards reads
type GetHeavyWorkload struct{}

func (w *GetHeavyWorkload) Name() string {
	return "get-heavy"
}

func (w *GetHeavyWorkload) Description() string {
	return "Read-heavy workload: 95% get, 5% set"
}

func (w *GetHeavyWorkload) Execute(ctx context.Context, client *memcache.Client, workerID int) error {
	key := fmt.Sprintf("key-%d", rand.IntN(1000))

	if rand.Float64() < 0.95 {
		// GET
		_, err := client.Get(ctx, key)
		if err != nil && err.Error() != "key not found" {
			return err
		}
	} else {
		// SET
		if err := client.Set(ctx, memcache.Item{
			Key:   key,
			Value: bytes.Repeat([]byte("A"), 100),
			TTL:   60 * time.Second,
		}); err != nil {
			return err
		}
	}

	return nil
}

// SetHeavyWorkload is heavily weighted towards writes
type SetHeavyWorkload struct{}

func (w *SetHeavyWorkload) Name() string {
	return "set-heavy"
}

func (w *SetHeavyWorkload) Description() string {
	return "Write-heavy workload: 20% get, 80% set"
}

func (w *SetHeavyWorkload) Execute(ctx context.Context, client *memcache.Client, workerID int) error {
	key := fmt.Sprintf("key-worker%d-%d", workerID, rand.IntN(100))

	if rand.Float64() < 0.20 {
		// GET
		_, err := client.Get(ctx, key)
		if err != nil && err.Error() != "key not found" {
			return err
		}
	} else {
		// SET
		value := []byte(fmt.Sprintf("value-%d-%d", workerID, time.Now().UnixNano()))
		if err := client.Set(ctx, memcache.Item{
			Key:   key,
			Value: value,
			TTL:   30 * time.Second,
		}); err != nil {
			return err
		}
	}

	return nil
}
