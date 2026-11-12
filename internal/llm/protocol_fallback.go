package llm

import (
	"crypto/tls"
	"net/http"
	"strings"
)

func needsHTTP1Retry(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "stream error: stream id") && strings.Contains(text, "received from peer") ||
		strings.Contains(text, "http2: client connection lost") ||
		strings.Contains(text, "read responses stream") && (strings.Contains(text, "stream inactivity timeout") || strings.Contains(text, "read tcp") || strings.Contains(text, "unexpected eof"))
}

func http1FallbackClient(original *http.Client) (*http.Client, func()) {
	roundTripper := original.Transport
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	transport, ok := roundTripper.(*http.Transport)
	if !ok {
		return nil, nil
	}
	transport = transport.Clone()
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	client := *original
	client.Transport = transport
	return &client, transport.CloseIdleConnections
}
