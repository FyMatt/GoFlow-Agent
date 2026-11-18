package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

type researchOptions struct {
	refresh        bool
	allowedDomains []string
	contentChars   int
}

// Only public-document requests use this transport. Authorized scan tools retain
// their separate client and scope checks. Tests replace it with a local fixture transport.
var publicDocsTransport http.RoundTripper = &http.Transport{
	DialContext:           publicDocumentDial,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 12 * time.Second,
}

// researchAllowsLocalAddresses is an explicit test/acceptance affordance: the
// P7 research case serves its fixture from loopback, which the public-document
// guard otherwise rejects. It is off unless the environment opts in.
func researchAllowsLocalAddresses() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOFLOW_RESEARCH_ALLOW_LOCAL"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func publicDocumentDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if researchAllowsLocalAddresses() {
		var localDialer net.Dialer
		return localDialer.DialContext(ctx, network, address)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for public document host")
	}
	for _, ip := range ips {
		if !publicDocumentIP(ip) {
			return nil, fmt.Errorf("public documents cannot access non-public address %s", ip)
		}
	}
	// Dial the checked IP directly to avoid a second DNS resolution.
	var dialer net.Dialer
	for _, ip := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	return nil, err
}

func publicDocumentIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	// Special-use networks that are global unicast according to netip.
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func parseResearchOptions(args map[string]any) (researchOptions, error) {
	var options researchOptions
	if value, exists := args["refresh"]; exists {
		var ok bool
		options.refresh, ok = value.(bool)
		if !ok {
			return options, fmt.Errorf("refresh must be a boolean")
		}
	}
	if value, exists := args["allowed_domains"]; exists {
		values, ok := value.([]any)
		if !ok || len(values) == 0 || len(values) > 20 {
			return options, fmt.Errorf("allowed_domains must contain 1 to 20 domain names")
		}
		for _, value := range values {
			domain, ok := value.(string)
			domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
			parsed, err := url.Parse("https://" + domain)
			if !ok || err != nil || domain == "" || parsed.Hostname() != domain || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(domain, "/*?#@:%\\") {
				return options, fmt.Errorf("allowed_domains entries must be plain domain names")
			}
			options.allowedDomains = append(options.allowedDomains, domain)
		}
	}
	return options, nil
}

func (options researchOptions) validateURL(raw string) (*url.URL, error) {
	parsed, err := validateHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	if parsed.User != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("public document URLs must have a host and no credentials")
	}
	if len(options.allowedDomains) == 0 {
		return parsed, nil
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	for _, domain := range options.allowedDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("domain %q is outside allowed_domains", host)
}

func fetchDocument(ctx context.Context, parsed *url.URL, maxBytes int, options researchOptions) (fetchedPage, error) {
	if _, err := options.validateURL(parsed.String()); err != nil {
		return fetchedPage{}, err
	}
	maxBytes = minInt(maxBytes, hardMaxFetchBytes)
	if maxBytes <= 0 {
		maxBytes = defaultMaxFetchBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fetchedPage{}, err
	}
	req.Header.Set("User-Agent", "GoFlow-Agent/1.0")
	if options.refresh {
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	}
	client := *httpClient
	client.Transport = publicDocsTransport
	if client.Timeout <= 0 || client.Timeout > 12*time.Second {
		client.Timeout = 12 * time.Second
	}
	chain := []string{parsed.String()}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if _, err := options.validateURL(req.URL.String()); err != nil {
			return err
		}
		if httpClient.CheckRedirect != nil {
			if err := httpClient.CheckRedirect(req, via); err != nil {
				return err
			}
		}
		chain = append(chain, req.URL.String())
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		// A rejected redirect still has an auditable HTTP response. Its body is
		// already closed by net/http; do not treat it as document evidence.
		if resp != nil {
			return fetchedPage{status: resp.StatusCode, finalURL: resp.Request.URL.String(), fetchedAt: time.Now().UTC().Format(time.RFC3339Nano), redirectChain: chain}, err
		}
		return fetchedPage{}, err
	}
	defer resp.Body.Close()
	page := fetchedPage{
		status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), etag: resp.Header.Get("ETag"),
		lastModified: resp.Header.Get("Last-Modified"), responseDate: resp.Header.Get("Date"),
		finalURL: resp.Request.URL.String(), fetchedAt: time.Now().UTC().Format(time.RFC3339Nano), redirectChain: chain,
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return page, err
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	page.content, page.bytesRead, page.truncated = string(body), len(body), truncated
	page.fetchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return page, nil
}
