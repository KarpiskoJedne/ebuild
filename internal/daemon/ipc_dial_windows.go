//go:build windows

package daemon

import (
	"fmt"
	"net"
	"time"
)

// Windows Named Pipes: Go's stdlib net has no pipe dialer, so the Windows
// daemon also listens on 127.0.0.1 with a port derived from the pipe name.
// The pipe path \\.\pipe\<name> maps to 127.0.0.1:<base+hash%10000>.
func dialPlatform(endpoint string) (net.Conn, error) {
	port := 44731
	for _, r := range endpoint {
		port += int(r) % 97
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	return d.Dial("tcp", addr)
}
