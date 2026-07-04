package memcache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dynamicServers is a mutable, concurrency-safe Servers used to simulate a
// server set that changes at runtime (e.g. Kubernetes endpoint churn).
type dynamicServers struct {
	mu   sync.Mutex
	list []Server
}

func newDynamicServers(addrs ...string) *dynamicServers {
	d := &dynamicServers{}
	d.set(addrs...)
	return d
}

func (d *dynamicServers) set(addrs ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.list = make([]Server, len(addrs))
	for i, addr := range addrs {
		d.list[i] = Server{Address: addr}
	}
}

func (d *dynamicServers) List() []Server {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Server(nil), d.list...)
}

func (c *Client) poolAddrs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	addrs := make([]string, 0, len(c.pools))
	for addr := range c.pools {
		addrs = append(addrs, addr)
	}
	return addrs
}

func TestReapDepartedPools(t *testing.T) {
	const addrA, addrB = "a:11211", "b:11211"

	newClient := func(servers Servers) *Client {
		// A mock dialer lets Acquire create real pooled connections without a
		// network; HealthCheckInterval is left at zero so the loop never fires
		// on its own and the test drives reaping deterministically.
		client := NewClient(servers, Config{
			MaxSize: 2,
			Timeout: time.Second,
			Dialer:  &mockDialer{conn: testutils.NewConnectionMock()},
		})
		t.Cleanup(client.Close)
		return client
	}

	t.Run("departed server pool is closed and forgotten", func(t *testing.T) {
		servers := newDynamicServers(addrA, addrB)
		client := newClient(servers)

		spA, err := client.getPoolForServer(addrA)
		require.NoError(t, err)
		spB, err := client.getPoolForServer(addrB)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{addrA, addrB}, client.poolAddrs())

		servers.set(addrA)
		client.checkAllPools()

		assert.Equal(t, []string{addrA}, client.poolAddrs(), "only the live server's pool should remain")

		// The surviving pool is still usable; the departed one is closed.
		res, err := spA.pool.Acquire(context.Background())
		require.NoError(t, err, "live pool must stay usable")
		res.Release() // return it so Close in cleanup doesn't block
		_, err = spB.pool.Acquire(context.Background())
		assert.Error(t, err, "departed pool must be closed")
	})

	t.Run("empty server set is not reaped", func(t *testing.T) {
		servers := newDynamicServers(addrA, addrB)
		client := newClient(servers)

		_, err := client.getPoolForServer(addrA)
		require.NoError(t, err)
		_, err = client.getPoolForServer(addrB)
		require.NoError(t, err)

		// A discovery blip momentarily reports no servers: pools must survive.
		servers.set()
		client.checkAllPools()

		assert.ElementsMatch(t, []string{addrA, addrB}, client.poolAddrs(),
			"a transient empty set must not tear down healthy pools")
	})
}
