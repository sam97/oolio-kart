package healthcheck

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProbe(t *testing.T) {
	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" || !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	if err := Probe(server.Listener.Addr().String()); err == nil {
		t.Error("Probe succeeded while not ready")
	}

	ready.Store(true)
	for _, addr := range []string{
		server.Listener.Addr().String(),
		":" + port,        // every interface, as containers listen
		"0.0.0.0:" + port, // same, spelled out
		"[::]:" + port,    // IPv6 any
	} {
		if err := Probe(addr); err != nil {
			t.Errorf("Probe(%q) = %v", addr, err)
		}
	}

	if err := Probe("no-port"); err == nil {
		t.Error("Probe accepted an address without a port")
	}
}
