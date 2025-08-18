package memcache

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/pior/memcache/protocol"
)

type Querier interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta int64) (int64, error)
	Decrement(ctx context.Context, key string, delta int64) (int64, error)
}

func NewQuerier(client *Client) Querier {
	return &querier{
		client: client,
	}
}

type querier struct {
	client *Client
}

// Get retrieves a value for a key. Returns ErrCacheMiss if not found.
func (q *querier) Get(ctx context.Context, key string) ([]byte, error) {
	cmd := NewGetCommand(key)
	if err := q.client.ExecutorWait(ctx, cmd); err != nil {
		return nil, err
	}
	if cmd.Response == nil || cmd.Response.Error != nil {
		if cmd.Response != nil {
			return nil, cmd.Response.Error
		}
		return nil, nil
	}
	return cmd.Response.Value, nil
}

// Set stores a value for a key with an optional TTL.
func (q *querier) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := NewSetCommand(key, value, ttl)
	return q.client.ExecutorWait(ctx, cmd)
}

// Delete removes a key from the cache. Returns ErrCacheMiss if not found.
func (q *querier) Delete(ctx context.Context, key string) error {
	cmd := NewDeleteCommand(key)
	return q.client.ExecutorWait(ctx, cmd)
}

// Increment increases a numeric value by delta. Returns new value or ErrCacheMiss if not found.
func (q *querier) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	cmd := NewIncrementCommand(key, delta)
	if err := q.client.ExecutorWait(ctx, cmd); err != nil {
		return 0, err
	}
	return handleArithmeticResponseValue(cmd.Response)
}

// Decrement decreases a numeric value by delta. Returns new value or ErrCacheMiss if not found.
func (q *querier) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	cmd := NewDecrementCommand(key, delta)
	if err := q.client.ExecutorWait(ctx, cmd); err != nil {
		return 0, err
	}
	return handleArithmeticResponseValue(cmd.Response)
}

func handleArithmeticResponseValue(resp *protocol.Response) (int64, error) {
	if resp == nil || resp.Error != nil {
		if resp != nil {
			return 0, resp.Error
		}
		return 0, nil
	}
	val, convErr := strconv.ParseInt(string(resp.Value), 10, 64)
	if convErr != nil {
		return 0, errors.Join(errors.New("failed to parse response value"), convErr)
	}
	return val, nil
}
