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

func poolAddrs(c *Client) []string {
	metrics := c.PoolMetrics()
	addrs := make([]string, 0, len(metrics))
	for _, m := range metrics {
		addrs = append(addrs, m.Addr)
	}
	return addrs
}

func TestReapDepartedPools(t *testing.T) {
	const addrA, addrB = "a:11211", "b:11211"

	newClient := func(servers Servers) *Client {
		// A mock dialer lets Acquire create real pooled connections without a
		// network; the maintenance loop always runs, but a long interval keeps
		// it dormant so it never fires on its own and the test drives reaping
		// deterministically via runMaintenancePass.
		client := NewClient(servers, Config{
			MaxSize:             2,
			Timeout:             time.Second,
			MaintenanceInterval: time.Hour,
			Dialer:              &mockDialer{conn: testutils.NewConnectionMock()},
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
		assert.ElementsMatch(t, []string{addrA, addrB}, poolAddrs(client))

		servers.set(addrA)
		client.runMaintenancePass()
		assert.ElementsMatch(t, []string{addrA, addrB}, poolAddrs(client),
			"one absent pass is within the grace period")

		client.runMaintenancePass()
		assert.Equal(t, []string{addrA}, poolAddrs(client), "only the live server's pool should remain")

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

		// A discovery blip reports no servers, even across enough passes to
		// exceed the grace period: pools must survive.
		servers.set()
		client.runMaintenancePass()
		client.runMaintenancePass()

		assert.ElementsMatch(t, []string{addrA, addrB}, poolAddrs(client),
			"a transient empty set must not tear down healthy pools")
	})

	t.Run("non-consecutive absent passes are not reaped", func(t *testing.T) {
		servers := newDynamicServers(addrA, addrB)
		client := newClient(servers)

		_, err := client.getPoolForServer(addrA)
		require.NoError(t, err)
		_, err = client.getPoolForServer(addrB)
		require.NoError(t, err)

		// One List() misses addrB, then it comes back, then another single
		// miss: the absence counter must reset on reappearance, so two
		// non-consecutive absent passes never reap.
		servers.set(addrA)
		client.runMaintenancePass()
		servers.set(addrA, addrB)
		client.runMaintenancePass()
		servers.set(addrA)
		client.runMaintenancePass()

		assert.ElementsMatch(t, []string{addrA, addrB}, poolAddrs(client),
			"a discovery blip missing one server must not reap its pool")
	})
}

// TestBackgroundLoopReapsDepartedPools is the regression for the always-on
// maintenance loop: with no manual runMaintenancePass calls, the background loop
// reaps a departed-server pool on its own so a dynamic server set does not leak.
// A non-positive MaintenanceInterval selects the default; here a short positive
// interval lets the loop actually fire within the test.
func TestBackgroundLoopReapsDepartedPools(t *testing.T) {
	const addrA, addrB = "a:11211", "b:11211"

	servers := newDynamicServers(addrA, addrB)
	client := NewClient(servers, Config{
		MaxSize:             2,
		Timeout:             time.Second,
		MaintenanceInterval: 20 * time.Millisecond,
		Dialer:              &mockDialer{conn: testutils.NewConnectionMock()},
	})
	t.Cleanup(client.Close)

	_, err := client.getPoolForServer(addrA)
	require.NoError(t, err)
	_, err = client.getPoolForServer(addrB)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{addrA, addrB}, poolAddrs(client))

	servers.set(addrA) // addrB departs

	assert.Eventually(t, func() bool {
		addrs := poolAddrs(client)
		return len(addrs) == 1 && addrs[0] == addrA
	}, 2*time.Second, 5*time.Millisecond,
		"the always-on background loop must reap the departed pool on its own")
}
