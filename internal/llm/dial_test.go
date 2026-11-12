package llm

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

// A dial to an IPv4-only listener proves the dialer tries IPv4 first: the same
// address cannot be reached over tcp6.
func TestIPv4FirstDialContextReachesIPv4OnlyListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no IPv4 loopback available: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := ipv4FirstDialContext(&net.Dialer{})(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("IPv4-first dial must reach an IPv4 listener: %v", err)
	}
	_ = conn.Close()
}

func TestClientInstallsIPv4FirstTransport(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://provider.invalid", APIKey: "test-key"})
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("client must use an HTTP transport, got %T", client.httpClient.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("client transport must install the IPv4-first dialer")
	}
}
