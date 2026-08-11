package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// requiredTools lists external tools that covo-agent can auto-download.
var requiredTools = []struct {
	Name    string
	Binary  string
	URLTmpl string
}{
	{
		Name:    "ripgrep (rg)",
		Binary:  "rg",
		URLTmpl: "https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-{arch}-{os}.tar.gz",
	},
	{
		Name:    "fd",
		Binary:  "fd",
		URLTmpl: "https://github.com/sharkdp/fd/releases/download/v10.2.0/fd-v10.2.0-{arch}-{os}.tar.gz",
	},
}

// autoInstallTools checks for required tools and installs missing ones.
func AutoInstallTools(homeDir string) {
	for _, tool := range requiredTools {
		if _, err := exec.LookPath(tool.Binary); err == nil {
			continue
		}
		fmt.Printf("  Installing %s... ", tool.Name)
		if err := downloadAndInstall(tool, homeDir); err != nil {
			fmt.Printf("failed: %v\n", err)
		} else {
			fmt.Printf("done\n")
		}
	}
}

func downloadAndInstall(tool struct {
	Name    string
	Binary  string
	URLTmpl string
}, homeDir string) error {
	arch := map[string]string{
		"amd64": "x86_64",
		"arm64": "aarch64",
	}[runtime.GOARCH]
	osName := map[string]string{
		"darwin": "apple-darwin",
		"linux":  "unknown-linux-gnu",
	}[runtime.GOOS]

	if arch == "" || osName == "" {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	url := strings.NewReplacer("{arch}", arch, "{os}", osName).Replace(tool.URLTmpl)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()

	toolDir := filepath.Join(homeDir, "bin")
	if err := os.MkdirAll(toolDir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	targetPath := filepath.Join(toolDir, tool.Binary)

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if strings.HasSuffix(header.Name, "/"+tool.Binary) {
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("create: %w", err)
			}
			defer out.Close()
			if _, err := io.Copy(out, tr); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			break
		}
	}

	// Add to PATH if not already (bin dir check)
	return nil
}

// ensureBinInPath ensures homeDir/bin is in PATH.
func EnsureBinInPath(homeDir string) {
	binDir := filepath.Join(homeDir, "bin")
	currentPath := os.Getenv("PATH")
	if strings.Contains(currentPath, binDir) {
		return
	}
	os.Setenv("PATH", binDir+string(os.PathListSeparator)+currentPath)
}
