package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
)

func TestCyberHTTPRejectsCertificateHostnameMismatch(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Error("hostname mismatch reached HTTP handler")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	for protocol, transport := range cyberHTTPTransports(nil) {
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}
		request, err := http.NewRequest(http.MethodGet, "https://wrong-host.invalid/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := transport.RoundTrip(request)
		transport.CloseIdleConnections()
		if response != nil {
			response.Body.Close()
		}
		var hostnameError x509.HostnameError
		if !errors.As(err, &hostnameError) || hostnameError.Host != "wrong-host.invalid" {
			t.Fatalf("%s: expected hostname mismatch, got %v", protocol, err)
		}
	}
}

func TestCyberHTTPProtocolAndCertificateIsolation(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	observed := make(chan bool, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed <- len(request.TLS.PeerCertificates) > 0
		writer.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	certificate := server.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	untrusted := cyberHTTPTransports(nil)["auto"]
	untrusted.DialContext = (&net.Dialer{}).DialContext
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	if response, err := untrusted.RoundTrip(request); err == nil {
		response.Body.Close()
		t.Fatal("untrusted TLS certificate accepted")
	}
	untrusted.CloseIdleConnections()
	for _, useCertificate := range []bool{false, true} {
		var selected *tls.Certificate
		if useCertificate {
			selected = &certificate
		}
		transports := cyberHTTPTransports(selected)
		for protocol, transport := range transports {
			if transport.Proxy != nil || transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
				t.Fatal("unsafe transport default")
			}
			transport.DialContext = (&net.Dialer{}).DialContext
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			transport.TLSClientConfig.RootCAs = pool
			request, _ := http.NewRequest(http.MethodGet, server.URL+"/a/../b/%2f", nil)
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(protocol, err)
			}
			response.Body.Close()
			transport.CloseIdleConnections()
			if protocol == "http1" && response.ProtoMajor != 1 || protocol == "http2" && response.ProtoMajor != 2 {
				t.Fatal("unexpected protocol", protocol, response.Proto)
			}
			if actual := <-observed; actual != useCertificate {
				t.Fatal("certificate selection mismatch")
			}
		}
	}
}

func TestCyberHTTPRejectsUnsafeTargetsAndCertificateConfiguration(t *testing.T) {
	t.Setenv("COVO_CYBER_CLIENT_CERT", "")
	t.Setenv("COVO_CYBER_CLIENT_KEY", "")
	transport, err := NewCyberHTTPTransport()
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range []cyber.HTTPOptions{{Protocol: "http2"}, {Protocol: "http1"}, {ClientCertificate: true}} {
		request, _ := http.NewRequestWithContext(cyber.WithHTTPOptions(context.Background(), options), http.MethodGet, "https://127.0.0.1:9", nil)
		if _, err := transport.RoundTrip(request); err == nil {
			t.Fatal("private target accepted")
		}
	}
	t.Setenv("COVO_CYBER_CLIENT_CERT", "missing")
	if _, err := NewCyberHTTPTransport(); err == nil {
		t.Fatal("incomplete client credentials accepted")
	}
}
