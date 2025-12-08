package workload

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/pior/memcache"
)

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
	if rand.Float64() < 0.3 { // 30% chance of hot key
		key = fmt.Sprintf("hot-key-%d", rand.IntN(10))
	} else {
		key = fmt.Sprintf("key-worker%d-%d", workerID, rand.IntN(1000))
	}

	// Choose operation based on probability
	op := rand.Float64()

	switch {
	case op < 0.60: // 60% GET
		_, err := client.Get(ctx, key)
		// Ignore "not found" errors - they're expected
		if err != nil && err.Error() != "key not found" {
			return err
		}

	case op < 0.90: // 30% SET
		value := []byte(fmt.Sprintf("value-%d-%d", workerID, time.Now().UnixNano()))
		ttl := time.Duration(30+rand.IntN(60)) * time.Second
		if err := client.Set(ctx, memcache.Item{
			Key:   key,
			Value: value,
			TTL:   ttl,
		}); err != nil {
			return err
		}

	case op < 0.95: // 5% DELETE
		// Ignore errors - key might not exist
		_ = client.Delete(ctx, key)

	default: // 5% INCREMENT
		counterKey := fmt.Sprintf("counter-worker%d", workerID)
		_, err := client.Increment(ctx, counterKey, 1, memcache.NoTTL)
		// Ignore "not found" - counter might not exist yet
		if err != nil && err.Error() != "key not found" {
			return err
		}
	}

	return nil
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
		value := []byte(fmt.Sprintf("value-%d", time.Now().UnixNano()))
		if err := client.Set(ctx, memcache.Item{
			Key:   key,
			Value: value,
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
