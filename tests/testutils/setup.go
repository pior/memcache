package testutils

import (
	"context"
	"fmt"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/pior/memcache"
	"github.com/sony/gobreaker/v2"
)

// ToxiproxyConfig holds toxiproxy setup configuration
type ToxiproxyConfig struct {
	APIAddr string
	Proxies []ProxyConfig
}

// ProxyConfig defines a single proxy
type ProxyConfig struct {
	Name     string
	Listen   string
	Upstream string
}

// DefaultToxiproxyConfig returns the standard 3-node setup
func DefaultToxiproxyConfig() ToxiproxyConfig {
	return ToxiproxyConfig{
		APIAddr: "http://localhost:8474",
		Proxies: []ProxyConfig{
			{Name: "memcache1", Listen: "0.0.0.0:21211", Upstream: "memcache1:11211"},
			{Name: "memcache2", Listen: "0.0.0.0:21212", Upstream: "memcache2:11211"},
			{Name: "memcache3", Listen: "0.0.0.0:21213", Upstream: "memcache3:11211"},
		},
	}
}

// SetupToxiproxy creates and configures toxiproxy proxies
func SetupToxiproxy(config ToxiproxyConfig) (*toxiproxy.Client, []*toxiproxy.Proxy, error) {
	client := toxiproxy.NewClient(config.APIAddr)

	// Wait for toxiproxy to be ready
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("timeout waiting for toxiproxy to be ready")
		default:
			proxies, err := client.Proxies()
			if err == nil {
				// Toxiproxy is ready, clean up any existing proxies
				for _, proxy := range proxies {
					_ = proxy.Delete()
				}
				goto ready
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

ready:
	// Create proxies
	proxies := make([]*toxiproxy.Proxy, 0, len(config.Proxies))
	for _, pConfig := range config.Proxies {
		proxy, err := client.CreateProxy(pConfig.Name, pConfig.Listen, pConfig.Upstream)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create proxy %s: %w", pConfig.Name, err)
		}
		proxies = append(proxies, proxy)
		fmt.Printf("[Setup] Created proxy: %s (%s -> %s)\n", pConfig.Name, pConfig.Listen, pConfig.Upstream)
	}

	// Verify proxies are working
	for _, proxy := range proxies {
		if err := proxy.Enable(); err != nil {
			return nil, nil, fmt.Errorf("failed to enable proxy %s: %w", proxy.Name, err)
		}
	}

	return client, proxies, nil
}

// CleanupToxiproxy removes all toxics and resets proxies
func CleanupToxiproxy(proxies []*toxiproxy.Proxy) error {
	for _, proxy := range proxies {
		// Remove all toxics
		toxics, err := proxy.Toxics()
		if err != nil {
			continue
		}
		for _, toxic := range toxics {
			_ = proxy.RemoveToxic(toxic.Name)
		}

		// Ensure proxy is enabled
		_ = proxy.Enable()
	}
	return nil
}

// MemcacheClientConfig holds memcache client configuration
type MemcacheClientConfig struct {
	Servers                []string
	PoolSize               int32
	MaxConnLifetime        time.Duration
	MaxConnIdleTime        time.Duration
	HealthCheckInterval    time.Duration
	CircuitBreakerSettings *gobreaker.Settings
}

// DefaultMemcacheClientConfig returns standard client config for reliability testing
func DefaultMemcacheClientConfig() MemcacheClientConfig {
	return MemcacheClientConfig{
		Servers: []string{
			"localhost:21211",
			"localhost:21212",
			"localhost:21213",
		},
		PoolSize:            20,
		MaxConnLifetime:     1 * time.Minute,
		MaxConnIdleTime:     30 * time.Second,
		HealthCheckInterval: 1 * time.Second,
		CircuitBreakerSettings: &gobreaker.Settings{
			MaxRequests:  3,
			Interval:     30 * time.Second,
			Timeout:      5 * time.Second,
			BucketPeriod: 10 * time.Second,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return counts.Requests >= 10 && failureRatio >= 0.3
			},
			// OnStateChange will be set by the caller to capture state transitions
		},
	}
}

// SetupMemcacheClient creates a memcache client with the given config
func SetupMemcacheClient(config MemcacheClientConfig) (*memcache.Client, error) {
	servers := memcache.NewStaticServers(config.Servers...)

	client, err := memcache.NewClient(servers, memcache.Config{
		MaxSize:                config.PoolSize,
		MaxConnLifetime:        config.MaxConnLifetime,
		MaxConnIdleTime:        config.MaxConnIdleTime,
		HealthCheckInterval:    config.HealthCheckInterval,
		CircuitBreakerSettings: config.CircuitBreakerSettings,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create memcache client: %w", err)
	}

	fmt.Printf("[Setup] Created memcache client with %d servers\n", len(config.Servers))
	return client, nil
}

// WaitForHealthy waits for the memcache client to be able to connect to at least one server
func WaitForHealthy(ctx context.Context, client *memcache.Client) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		// Try a simple set operation
		err := client.Set(ctx, memcache.Item{
			Key:   "health-check",
			Value: []byte("ok"),
			TTL:   10 * time.Second,
		})
		if err == nil {
			fmt.Println("[Setup] Client is healthy")
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for client to be healthy")
}
