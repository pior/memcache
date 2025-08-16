package memcache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelector(t *testing.T) {
	serverAddresses := []string{"127.0.0.1:1001", "127.0.0.1:1002", "127.0.0.1:1003"}

	server := DefaultSelector(serverAddresses, "key1")
	require.Equal(t, "127.0.0.1:1002", server)

	server = DefaultSelector(serverAddresses, "key2")
	require.Equal(t, "127.0.0.1:1002", server)

	server = DefaultSelector(serverAddresses, "key3")
	require.Equal(t, "127.0.0.1:1001", server)

	server = DefaultSelector(serverAddresses, "key4")
	require.Equal(t, "127.0.0.1:1001", server)

	server = DefaultSelector(serverAddresses, "key5")
	require.Equal(t, "127.0.0.1:1002", server)

	server = DefaultSelector(serverAddresses, "key6")
	require.Equal(t, "127.0.0.1:1002", server)

	server = DefaultSelector(serverAddresses, "key7")
	require.Equal(t, "127.0.0.1:1003", server)

}
