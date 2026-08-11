package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https allowed", url: "https://example.com", wantErr: false},
		{name: "https github", url: "https://github.com/covoyage/covo-agent", wantErr: false},
		{name: "https with path", url: "https://github.com/covoyage/covo-agent/blob/main/README.md", wantErr: false},
		{name: "file blocked", url: "file:///etc/passwd", wantErr: true},
		{name: "ftp blocked", url: "ftp://evil.com/malware", wantErr: true},
		{name: "localhost blocked", url: "http://localhost:8080", wantErr: true},
		{name: "127.0.0.1 blocked", url: "http://127.0.0.1/api", wantErr: true},
		{name: "0.0.0.0 blocked", url: "http://0.0.0.0:3000", wantErr: true},
		{name: "10.x blocked", url: "http://10.0.0.1/internal", wantErr: true},
		{name: "172.16.x blocked", url: "http://172.16.0.1/", wantErr: true},
		{name: "192.168.x blocked", url: "http://192.168.1.1/admin", wantErr: true},
		{name: "169.254.x blocked", url: "http://169.254.169.254/latest/meta-data/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error=%v wantErr=%v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "safety-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "workspace")
	os.MkdirAll(subDir, 0755)

	tests := []struct {
		name    string
		base    string
		rel     string
		wantErr bool
	}{
		{name: "safe within base", base: subDir, rel: "file.txt", wantErr: false},
		{name: "safe subdirectory", base: subDir, rel: "src/main.go", wantErr: false},
		{name: "traversal blocked", base: subDir, rel: "../etc/passwd", wantErr: true},
		{name: "absolute blocked", base: subDir, rel: "/etc/passwd", wantErr: true},
		{name: "double dot traversal", base: subDir, rel: "../../root/.bashrc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidatePath(tt.base, tt.rel)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath(%q, %q) error=%v wantErr=%v", tt.base, tt.rel, err, tt.wantErr)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"100.100.100.200", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := IsPrivateIP(tt.ip)
			if got != tt.want {
				t.Errorf("IsPrivateIP(%q)=%v want=%v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsSensitivePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/etc/passwd", true},
		{"/etc/shadow", true},
		{"/root/.ssh/id_rsa", true},
		{"/home/user/.ssh/id_rsa", true},
		{"/Users/me/.aws/credentials", true},
		{"/proc/cpuinfo", true},
		{"/var/log/app.log", false},
		{"/home/user/projects/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsSensitivePath(tt.path)
			if got != tt.want {
				t.Errorf("IsSensitivePath(%q)=%v want=%v", tt.path, got, tt.want)
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "strip credentials",
			raw:  "https://user:pass@example.com/secret",
			want: "https://example.com/secret",
		},
		{
			name: "clean URL unchanged",
			raw:  "https://example.com/path",
			want: "https://example.com/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeURL(tt.raw)
			if got != tt.want {
				t.Errorf("SanitizeURL(%q)=%q want=%q", tt.raw, got, tt.want)
			}
		})
	}
}
