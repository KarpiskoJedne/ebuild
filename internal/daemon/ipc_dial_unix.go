//go:build !windows

package daemon

import (
	"net"
	"time"
)

func dialPlatform(endpoint string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.Dial("unix", endpoint)
}
