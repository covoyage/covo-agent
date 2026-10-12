package cyber

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

type HTTPOptions struct {
	Protocol          string
	ClientCertificate bool
}

type httpOptionsKey struct{}

func WithHTTPOptions(ctx context.Context, options HTTPOptions) context.Context {
	return context.WithValue(ctx, httpOptionsKey{}, options)
}

func HTTPOptionsFromContext(ctx context.Context) HTTPOptions {
	options, _ := ctx.Value(httpOptionsKey{}).(HTTPOptions)
	return options
}

type HTTPDiagnostics struct {
	DNSMS              *int64 `json:"dns_ms,omitempty"`
	ConnectMS          *int64 `json:"connect_ms,omitempty"`
	TLSMS              *int64 `json:"tls_ms,omitempty"`
	TTFBMS             *int64 `json:"ttfb_ms,omitempty"`
	TotalMS            int64  `json:"total_ms"`
	ConnectionReused   bool   `json:"connection_reused"`
	TLSVersion         string `json:"tls_version,omitempty"`
	NegotiatedProtocol string `json:"negotiated_protocol,omitempty"`
}

func TraceHTTP(ctx context.Context) (context.Context, func() HTTPDiagnostics) {
	var mutex sync.Mutex
	started := time.Now()
	var dnsStarted, connectStarted, tlsStarted time.Time
	var diagnostics HTTPDiagnostics
	elapsed := func(start time.Time) *int64 { value := time.Since(start).Milliseconds(); return &value }
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { mutex.Lock(); defer mutex.Unlock(); dnsStarted = time.Now() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			mutex.Lock()
			defer mutex.Unlock()
			if !dnsStarted.IsZero() {
				diagnostics.DNSMS = elapsed(dnsStarted)
			}
		},
		ConnectStart: func(string, string) { mutex.Lock(); defer mutex.Unlock(); connectStarted = time.Now() },
		ConnectDone: func(string, string, error) {
			mutex.Lock()
			defer mutex.Unlock()
			if !connectStarted.IsZero() {
				diagnostics.ConnectMS = elapsed(connectStarted)
			}
		},
		TLSHandshakeStart: func() { mutex.Lock(); defer mutex.Unlock(); tlsStarted = time.Now() },
		TLSHandshakeDone: func(state tls.ConnectionState, _ error) {
			mutex.Lock()
			defer mutex.Unlock()
			if !tlsStarted.IsZero() {
				diagnostics.TLSMS = elapsed(tlsStarted)
			}
			diagnostics.TLSVersion = tls.VersionName(state.Version)
			diagnostics.NegotiatedProtocol = state.NegotiatedProtocol
		},
		GotConn: func(info httptrace.GotConnInfo) {
			mutex.Lock()
			defer mutex.Unlock()
			diagnostics.ConnectionReused = info.Reused
		},
		GotFirstResponseByte: func() { mutex.Lock(); defer mutex.Unlock(); diagnostics.TTFBMS = elapsed(started) },
	}
	return httptrace.WithClientTrace(ctx, trace), func() HTTPDiagnostics {
		mutex.Lock()
		defer mutex.Unlock()
		snapshot := diagnostics
		snapshot.TotalMS = time.Since(started).Milliseconds()
		return snapshot
	}
}
