package evolution

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseMCPLaunchPackage(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		eco     string
		pkg     string
		ver     string
		ok      bool
	}{
		{
			name:    "npx scoped with flags",
			command: "npx",
			args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			eco:     "npm",
			pkg:     "@modelcontextprotocol/server-filesystem",
			ok:      true,
		},
		{
			name:    "npx versioned",
			command: "/usr/local/bin/npx",
			args:    []string{"lodash@4.17.21"},
			eco:     "npm",
			pkg:     "lodash",
			ver:     "4.17.21",
			ok:      true,
		},
		{
			name:    "npx --package",
			command: "npx.cmd",
			args:    []string{"--package=@evil/pkg@1.0.0", "run-me"},
			eco:     "npm",
			pkg:     "@evil/pkg",
			ver:     "1.0.0",
			ok:      true,
		},
		{
			name:    "uvx extras and version",
			command: "uvx",
			args:    []string{"mcp-server-sqlite[cli]==0.2.0", "--db-path", "x"},
			eco:     "PyPI",
			pkg:     "mcp-server-sqlite",
			ver:     "0.2.0",
			ok:      true,
		},
		{
			name:    "pipx",
			command: "pipx",
			args:    []string{"some-tool"},
			eco:     "PyPI",
			pkg:     "some-tool",
			ok:      true,
		},
		{
			name:    "plain binary skipped",
			command: "python3",
			args:    []string{"-m", "mcp_server"},
			ok:      false,
		},
		{
			name:    "npx no package",
			command: "npx",
			args:    []string{"-y"},
			ok:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eco, pkg, ver, ok := ParseMCPLaunchPackage(tt.command, tt.args)
			if ok != tt.ok || eco != tt.eco || pkg != tt.pkg || ver != tt.ver {
				t.Fatalf("got eco=%q pkg=%q ver=%q ok=%v", eco, pkg, ver, ok)
			}
		})
	}
}

func TestCheckMCPCommandMalwareBlocksMAL(t *testing.T) {
	resetMCPOSVCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vulns": []map[string]any{
				{"id": "CVE-2024-1", "summary": "regular cve"},
				{"id": "MAL-2024-999", "summary": "typosquat installer"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	orig := mcpOSVEndpoint
	mcpOSVEndpoint = srv.URL
	t.Cleanup(func() { mcpOSVEndpoint = orig })

	err := CheckMCPCommandMalware("npx", []string{"-y", "evil-mcp"})
	if err == nil || !strings.Contains(err.Error(), "MAL-2024-999") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "evil-mcp") {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckMCPCommandMalwareIgnoresCVE(t *testing.T) {
	resetMCPOSVCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vulns": []map[string]any{
				{"id": "CVE-2024-1", "summary": "regular cve"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	orig := mcpOSVEndpoint
	mcpOSVEndpoint = srv.URL
	t.Cleanup(func() { mcpOSVEndpoint = orig })

	if err := CheckMCPCommandMalware("uvx", []string{"mcp-server-sqlite"}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckMCPCommandMalwareFailOpen(t *testing.T) {
	resetMCPOSVCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "nope")
	}))
	t.Cleanup(srv.Close)
	orig := mcpOSVEndpoint
	mcpOSVEndpoint = srv.URL
	t.Cleanup(func() { mcpOSVEndpoint = orig })

	if err := CheckMCPCommandMalware("npx", []string{"pkg"}); err != nil {
		t.Fatalf("fail-open want nil, got %v", err)
	}
}

func TestCheckMCPCommandMalwareCaches(t *testing.T) {
	resetMCPOSVCache()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"vulns": []any{}})
	}))
	t.Cleanup(srv.Close)
	orig := mcpOSVEndpoint
	mcpOSVEndpoint = srv.URL
	t.Cleanup(func() { mcpOSVEndpoint = orig })

	if err := CheckMCPCommandMalware("npx", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckMCPCommandMalware("npx", []string{"-y", "pkg"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}
}

func TestCheckMCPCommandMalwareSkipsNonNpx(t *testing.T) {
	if err := CheckMCPCommandMalware("node", []string{"server.js"}); err != nil {
		t.Fatal(err)
	}
}
