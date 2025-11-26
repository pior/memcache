package testutils

import "net"

type DialerDummy struct {
	Connection net.Conn
}

func (d *DialerDummy) DialContext(_ any, _, _ string) (net.Conn, error) {
	return d.Connection, nil
}
