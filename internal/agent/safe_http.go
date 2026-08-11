package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"

	"github.com/covoyage/covo-agent/internal/circuitbreaker"
)

// SafeRoundTripper wraps an http.RoundTripper and validates URLs before
// making requests, blocking access to internal / private / unsafe endpoints.
type SafeRoundTripper struct {
	transport http.RoundTripper
}

// RoundTrip implements http.RoundTripper. It validates the request URL before
// delegating to the wrapped transport. If the URL is unsafe, an error is
// returned without making the request.
//
// This is a cheap, fast-fail pre-check (scheme / hostname blocklist). The
// authoritative check happens in safeDialContext below, which pins the
// connection to the exact IP addresses that were validated — see its doc
// comment for why this matters.
func (s *SafeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ValidateURL(req.URL.String()); err != nil {
		return nil, fmt.Errorf("safe HTTP: %w", err)
	}
	return s.transport.RoundTrip(req)
}

// defaultBreakerRegistry is a package-level circuit breaker registry shared
// across all clients created via NewSafeClient. Breakers are keyed by host.
var defaultBreakerRegistry = circuitbreaker.NewRegistry(circuitbreaker.ClientPreset())

func init() {
	defaultBreakerRegistry.SetOnTrip(func(key string, stats circuitbreaker.Stats) {
		slog.Warn("circuit breaker opened",
			"host", key,
			"error_rate", fmt.Sprintf("%.1f%%", stats.ErrorRate*100),
			"failures", stats.Failures,
			"count", stats.Count,
		)
	})
}

var (
	safeClientOnce sync.Once
	safeClient     *http.Client
)

// NewSafeClient returns a singleton *http.Client configured with circuit
// breaker wrapping SafeRoundTripper wrapping a transport whose DialContext
// re-validates and pins DNS resolution. The transport chain is:
//
//	circuitbreaker.RoundTripper → SafeRoundTripper → http.Transport
func NewSafeClient() *http.Client {
	safeClientOnce.Do(func() {
		transport := &http.Transport{
			DialContext: safeDialContext,
		}
		safeTransport := &SafeRoundTripper{transport: transport}
		cbTransport := circuitbreaker.NewRoundTripper(safeTransport, defaultBreakerRegistry, nil)
		safeClient = &http.Client{
			Transport: cbTransport,
		}
	})
	return safeClient
}

// safeDialContext is a net.Dial-compatible DialContext that resolves addr's
// host, rejects the dial if ANY resolved IP falls in a blocked range, and
// then connects directly to one of the validated IP addresses.
//
// Why this is necessary: ValidateURL performs its own DNS lookup to decide
// whether a URL is safe, but if that decision is separate from the DNS
// lookup the transport later uses to actually connect, an attacker
// controlling the target's DNS records can return a public/safe IP for the
// validation lookup and a private/internal IP for the connection lookup
// (classic "DNS rebinding"), bypassing the SSRF protection entirely. Pinning
// the dial to the exact IP addresses that were just validated closes that
// TOCTOU window. TLS verification still uses the original hostname for SNI
// and certificate validation, so HTTPS correctness is unaffected.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("safe dial: %w", err)
	}

	var resolved []netip.Addr
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		resolved = []netip.Addr{ip.Unmap()}
	} else {
		ipAddrs, resolveErr := net.DefaultResolver.LookupIPAddr(ctx, host)
		if resolveErr != nil {
			return nil, fmt.Errorf("safe dial: resolve %q: %w", host, resolveErr)
		}
		for _, ia := range ipAddrs {
			if a, ok := netip.AddrFromSlice(ia.IP); ok {
				resolved = append(resolved, a.Unmap())
			}
		}
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("safe dial: no addresses resolved for %q", host)
	}
	for _, a := range resolved {
		if isBlockedIP(a) {
			return nil, fmt.Errorf("safe dial: %q resolves to a blocked address", host)
		}
	}

	var dialer net.Dialer
	var lastErr error
	for _, a := range resolved {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("safe dial: %w", lastErr)
}

// Ensure SafeRoundTripper implements http.RoundTripper.
var _ http.RoundTripper = (*SafeRoundTripper)(nil)
