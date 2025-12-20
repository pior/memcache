package testutils

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/pior/memcache"
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
// Use MEMCACHE_HOST environment variable to specify a remote memcache host (default: uses docker network names)
//
// For remote memcache servers, set MEMCACHE_HOST to the hostname or IP:
//
//	export MEMCACHE_HOST=misaki  # Hostname will be resolved to IP for Docker compatibility
//	export MEMCACHE_HOST=10.0.0.234  # Or use IP directly
func DefaultToxiproxyConfig() ToxiproxyConfig {
	// Allow configuring remote memcache host via environment variable
	memcacheHost := "memcache1" // Default: docker network name
	if host := os.Getenv("MEMCACHE_HOST"); host != "" {
		// Resolve hostname to IP address for Docker compatibility
		// (Docker containers can't resolve Bonjour names like "misaki.local")
		if resolvedIP := resolveHostToIP(host); resolvedIP != "" {
			log.Printf("[Setup] Resolved %s to %s for toxiproxy upstreams", host, resolvedIP)
			memcacheHost = resolvedIP
		} else {
			log.Printf("[Setup] Could not resolve %s, using as-is", host)
			memcacheHost = host
		}
	}

	return ToxiproxyConfig{
		APIAddr: "http://localhost:8474",
		Proxies: []ProxyConfig{
			{Name: "memcache1", Listen: "0.0.0.0:21211", Upstream: fmt.Sprintf("%s:11211", memcacheHost)},
			{Name: "memcache2", Listen: "0.0.0.0:21212", Upstream: fmt.Sprintf("%s:11212", memcacheHost)},
			{Name: "memcache3", Listen: "0.0.0.0:21213", Upstream: fmt.Sprintf("%s:11213", memcacheHost)},
		},
	}
}

// resolveHostToIP attempts to resolve a hostname to an IPv4 address
// Returns empty string if resolution fails or if input is already an IP
func resolveHostToIP(hostname string) string {
	// Check if it's already an IP address
	if net.ParseIP(hostname) != nil {
		return hostname
	}

	// Try to resolve hostname
	addrs, err := net.LookupHost(hostname)
	if err != nil || len(addrs) == 0 {
		return ""
	}

	// Return first IPv4 address
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			return ip.String()
		}
	}

	// Fall back to first address if no IPv4 found
	return addrs[0]
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
