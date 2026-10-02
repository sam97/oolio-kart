// Package healthcheck lets a service probe its own readiness endpoint. Container
// images without a shell or curl run the service binary itself as their health
// check.
package healthcheck

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// Timeout bounds a whole probe, so a hung server counts as unhealthy.
const Timeout = 3 * time.Second

// Probe GETs /ready from the server listening on addr and returns an error
// unless it answers 200. An addr that listens on every interface, like
// ":8080", is probed on loopback.
func Probe(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}

	client := http.Client{Timeout: Timeout}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/ready")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}
