package memcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

// ResilientProvider provides executors with full resilience features:
//   - Multi-server support with consistent hashing
//   - Circuit breakers for fault tolerance
//   - Connection pooling with health checks
//   - Lazy pool creation
//
// This is ideal for production multi-server deployments.
type ResilientProvider struct {
	servers           Servers
	selectServer      SelectServerFunc
	config            Config
	newCircuitBreaker func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response]

	// Multi-pool management
	mu    sync.RWMutex
	pools map[string]*serverPool

	// Health check management
	stopHealthCheck chan struct{}
}

// ResilientConfig extends Config with resilience-specific options.
type ResilientConfig struct {
	Config

	// SelectServer picks which server to use for a key.
	// If nil, uses DefaultSelectServer (CRC32-based).
	SelectServer SelectServerFunc

	// NewCircuitBreaker creates a circuit breaker for a server.
	// If nil, a no-op circuit breaker is used.
	NewCircuitBreaker func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response]
}

// NewResilientProvider creates a resilient connection provider for multiple servers.
func NewResilientProvider(servers Servers, config ResilientConfig) (*ResilientProvider, error) {
	selectServer := config.SelectServer
	if selectServer == nil {
		selectServer = DefaultSelectServer
	}

	// Validate servers
	serverList := servers.List()
	if len(serverList) == 0 {
		return nil, fmt.Errorf("no servers provided")
	}

	// Apply defaults to config
	if config.Dialer == nil {
		config.Dialer = &defaultDialer
	}
	if config.Pool == nil {
		config.Pool = NewChannelPool
	}
	if config.NewCircuitBreaker == nil {
		config.NewCircuitBreaker = func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response] {
			return defaultCircuitBreaker
		}
	}

	// Store in base Config
	config.Config.Dialer = config.Dialer
	config.Config.Pool = config.Pool

	provider := &ResilientProvider{
		servers:           servers,
		selectServer:      selectServer,
		config:            config.Config,
		newCircuitBreaker: config.NewCircuitBreaker,
		pools:             make(map[string]*serverPool),
		stopHealthCheck:   make(chan struct{}),
	}

	// Start health check goroutine if enabled
	if config.HealthCheckInterval > 0 {
		go provider.healthCheckLoop()
	}

	return provider, nil
}

// NewExecutor creates a new executor.
func (p *ResilientProvider) NewExecutor() Executor {
	return &resilientExecutor{
		provider: p,
	}
}

// Close closes all pools and stops health checks.
func (p *ResilientProvider) Close() error {
	// Stop health check goroutine if running
	if p.config.HealthCheckInterval > 0 {
		close(p.stopHealthCheck)
	}

	// Close all pools
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, sp := range p.pools {
		sp.pool.Close()
	}
	p.pools = nil

	return nil
}

// Stats returns statistics for all server pools.
func (p *ResilientProvider) Stats() interface{} {
	return p.AllPoolStats()
}

// AllPoolStats returns stats for all server pools.
func (p *ResilientProvider) AllPoolStats() []ServerPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := make([]ServerPoolStats, 0, len(p.pools))
	for _, sp := range p.pools {
		stats = append(stats, ServerPoolStats{
			Addr:                 sp.addr,
			PoolStats:            sp.pool.Stats(),
			CircuitBreakerState:  sp.circuitBreaker.State(),
			CircuitBreakerCounts: sp.circuitBreaker.Counts(),
		})
	}
	return stats
}

// getPoolForKey returns the pool for the server that should handle this key.
func (p *ResilientProvider) getPoolForKey(key string) (*serverPool, error) {
	servers := p.servers.List()
	addr, err := p.selectServer(key, servers)
	if err != nil {
		return nil, err
	}
	return p.getOrCreatePool(addr)
}

// getOrCreatePool gets or creates a pool for the given server address.
func (p *ResilientProvider) getOrCreatePool(addr string) (*serverPool, error) {
	// Fast path: read lock
	p.mu.RLock()
	sp, exists := p.pools[addr]
	p.mu.RUnlock()
	if exists {
		return sp, nil
	}

	// Slow path: write lock and create
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if sp, exists := p.pools[addr]; exists {
		return sp, nil
	}

	// Create new pool
	pool, cb, err := p.createPool(addr)
	if err != nil {
		return nil, err
	}

	sp = &serverPool{
		addr:           addr,
		pool:           pool,
		circuitBreaker: cb,
	}
	p.pools[addr] = sp
	return sp, nil
}

// createPool creates a new connection pool for a server.
func (p *ResilientProvider) createPool(addr string) (Pool, *gobreaker.CircuitBreaker[*meta.Response], error) {
	constructor := p.config.constructor
	if constructor == nil {
		constructor = func(ctx context.Context) (*Connection, error) {
			netConn, err := p.config.Dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return NewConnection(netConn), nil
		}
	}

	pool, err := p.config.Pool(constructor, p.config.MaxSize)
	if err != nil {
		return nil, nil, err
	}

	cb := p.newCircuitBreaker(addr)
	return pool, cb, nil
}

// healthCheckLoop periodically checks idle connections for health and lifecycle limits.
func (p *ResilientProvider) healthCheckLoop() {
	ticker := time.NewTicker(p.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopHealthCheck:
			return
		case <-ticker.C:
			p.healthCheckAllPools()
		}
	}
}

// healthCheckAllPools checks all pools for stale or unhealthy connections.
func (p *ResilientProvider) healthCheckAllPools() {
	p.mu.RLock()
	pools := make([]*serverPool, 0, len(p.pools))
	for _, sp := range p.pools {
		pools = append(pools, sp)
	}
	p.mu.RUnlock()

	for _, sp := range pools {
		p.checkPoolConnections(sp.pool)
	}
}

// checkPoolConnections checks all idle connections in a pool and destroys those that are stale or unhealthy.
func (p *ResilientProvider) checkPoolConnections(pool Pool) {
	now := time.Now()

	for _, res := range pool.AcquireAllIdle() {
		// Check max connection lifetime
		if p.config.MaxConnLifetime > 0 && now.Sub(res.CreationTime()) > p.config.MaxConnLifetime {
			res.Destroy()
			continue
		}

		// Check max idle time
		if p.config.MaxConnIdleTime > 0 && res.IdleDuration() > p.config.MaxConnIdleTime {
			res.Destroy()
			continue
		}

		// Perform health check by sending a noop command
		req := meta.NewRequest(meta.CmdNoOp, "", nil, nil)
		_, err := res.Value().Send(req)
		if err != nil {
			res.Destroy()
			continue
		}

		res.ReleaseUnused()
	}
}

// resilientExecutor uses circuit breakers and multi-server selection.
type resilientExecutor struct {
	provider *ResilientProvider
}

// Execute executes a request with circuit breaker protection and server selection.
func (e *resilientExecutor) Execute(ctx context.Context, key string, req *meta.Request) (*meta.Response, error) {
	// Select server pool based on key
	sp, err := e.provider.getPoolForKey(key)
	if err != nil {
		return nil, err
	}

	// Execute through circuit breaker
	return sp.circuitBreaker.Execute(func() (*meta.Response, error) {
		// Acquire connection from pool
		res, err := sp.pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}

		// Execute request
		resp, err := res.Value().Send(req)
		if err != nil {
			res.Destroy()
			return nil, err
		}

		// Return connection to pool
		res.Release()
		return resp, nil
	})
}
