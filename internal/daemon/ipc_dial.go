// Transport dialers per platform (same JSON frame protocol as ipc_client.go).
package daemon

import (
	"net"
)

func dialEndpoint(endpoint string) (net.Conn, error) {
	return dialPlatform(endpoint)
}
