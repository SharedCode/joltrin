package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

// validateImportURL rejects a client-supplied import URL that would let an
// authenticated user make this server fetch from its own internal network:
// loopback, RFC1918 private ranges, link-local (including the 169.254.169.254
// cloud metadata endpoint), and other non-routable addresses. Only http(s)
// URLs resolving to a public address are allowed.
func validateImportURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q did not resolve to any address", host)
	}
	for _, ip := range ips {
		if isDisallowedImportTarget(ip) {
			return fmt.Errorf("refusing to fetch from internal address %s (host %q)", ip, host)
		}
	}
	return nil
}

func isDisallowedImportTarget(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// ssrfSafeHTTPClient returns an http.Client whose transport re-resolves and
// re-validates the target of every connection it dials, including redirect
// hops. validateImportURL alone only checks the address at request-build
// time; a DNS answer can legitimately change between that check and the
// actual TCP connect (DNS rebinding), which would otherwise let a
// rebound name reach an internal address anyway.
func ssrfSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve host %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("host %q did not resolve to any address", host)
			}
			for _, ip := range ips {
				if isDisallowedImportTarget(ip) {
					return nil, fmt.Errorf("refusing to connect to internal address %s (host %q)", ip, host)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	return &http.Client{Transport: transport}
}
