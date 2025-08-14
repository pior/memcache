package protocol

import "errors"

var (
	ErrCacheMiss = errors.New("memcache: cache miss")
)
