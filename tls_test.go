package memcache

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestClientTLS verifies the documented TLS path: a *tls.Dialer plugged into
// Config establishes verified TLS connections to the server. The server cert
// is valid only for 127.0.0.1 and the client config sets no ServerName, so a
// successful handshake also proves ServerName is derived from the dial address.
func TestClientTLS(t *testing.T) {
	cert, roots := newSelfSignedCert(t)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go serveNoop(ln)

	addr := ln.Addr().String()

	ping := func(t *testing.T, tlsConfig *tls.Config) error {
		t.Helper()

		client := NewClient(StaticServers(addr), Config{
			MaxSize: 2,
			Timeout: 2 * time.Second,
			Dialer:  &tls.Dialer{Config: tlsConfig},
		})
		t.Cleanup(client.Close)

		sp, err := client.getPoolForServer(addr)
		require.NoError(t, err)

		res, err := sp.pool.Acquire(context.Background())
		if err != nil {
			return err
		}
		defer res.ReleaseUnused()

		return res.Value().Ping(context.Background())
	}

	t.Run("verified handshake with trusted CA", func(t *testing.T) {
		require.NoError(t, ping(t, &tls.Config{RootCAs: roots}))
	})

	t.Run("rejects untrusted certificate", func(t *testing.T) {
		// System roots do not trust the self-signed cert, so verification fails.
		err := ping(t, &tls.Config{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "certificate")
	})
}

// serveNoop accepts TLS connections and answers the meta no-op ("mn") used by
// Connection.Ping, which is enough to exercise a round-trip over the handshake.
func serveNoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			r := bufio.NewReader(c)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.HasPrefix(line, "mn") {
					_, _ = c.Write([]byte("MN\r\n"))
				}
			}
		}(conn)
	}
}

// newSelfSignedCert returns a self-signed certificate valid for 127.0.0.1 and
// a cert pool trusting it.
func newSelfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "memcache-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}, roots
}
