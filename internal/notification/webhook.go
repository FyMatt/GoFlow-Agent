package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func (p Publisher) deliver(ctx context.Context, event Event) (int, error) {
	if p.NetworkDisabled {
		return 0, errors.New("notification network access is disabled")
	}
	data, err := p.readBounded(filepath.Join("notifications", "channels", event.ChannelID+".json"), 16384)
	if err != nil {
		return 0, errors.New("saved channel profile is missing, unreadable or too large")
	}
	var channel Channel
	if decodeStrict(data, &channel) != nil || channel.Kind != "webhook" {
		return 0, errors.New("invalid saved webhook channel profile")
	}
	target, err := url.Parse(channel.URL)
	if err != nil || target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || target.Opaque != "" {
		return 0, errors.New("channel URL must be HTTP(S) without inline credentials, query or fragment")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, target.Hostname())
	if err != nil || len(addresses) == 0 {
		return 0, errors.New("channel destination could not be resolved")
	}
	allPrivate := true
	for _, address := range addresses {
		ip := address.IP
		allPrivate = allPrivate && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
		if !channel.AllowPrivate && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			return 0, errors.New("channel destination is private or local; user profile must explicitly allow_private")
		}
	}
	var token string
	if channel.CredentialRef != "" {
		path, err := config.SecretReferencePath(p.DataRoot, channel.CredentialRef)
		if err != nil {
			return 0, errors.New("channel credential_ref must be a valid secret:// reference")
		}
		relative, err := filepath.Rel(p.DataRoot, path)
		if err != nil {
			return 0, errors.New("channel credential reference is outside the data root")
		}
		secret, err := p.readBounded(relative, 4096)
		token = strings.TrimSpace(string(secret))
		if err != nil || token == "" || strings.ContainsAny(token, "\r\n") {
			return 0, errors.New("channel credential reference is missing or invalid")
		}
		if target.Scheme != "https" && !(channel.AllowPrivate && allPrivate) {
			return 0, errors.New("authenticated channel requires HTTPS")
		}
	}
	port := target.Port()
	if port == "" {
		port = "443"
		if target.Scheme == "http" {
			port = "80"
		}
	}
	// Dial the validated address directly, preserving the original Host and TLS
	// server name. No second DNS lookup, environment proxy or redirect can change it.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: DeliveryTimeout}).DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: DeliveryTimeout,
		MaxResponseHeaderBytes: 16384, DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: DeliveryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	body, err := json.Marshal(event)
	if err != nil || len(body) > MaxPayloadBytes {
		return 0, errors.New("encoded event payload is too large")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("invalid channel request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "GoFlow-Notification/1")
	request.Header.Set("Idempotency-Key", event.EventID)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("notification delivery interrupted: %w", ctx.Err())
		}
		// Transport errors can include URLs and credentials; never persist them.
		return 0, errors.New("notification delivery failed")
	}
	defer response.Body.Close() // Endpoint response bodies are never read or logged.
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("notification endpoint returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}
