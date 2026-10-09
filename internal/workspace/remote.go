package workspace

import (
	"fmt"
	"net/url"
	"strings"
)

// RemoteSpec describes a workspace that lives on another host. File tools
// and bash run against this location instead of the local cwd.
type RemoteSpec struct {
	Kind    string // ssh, wsl, docker
	Host    string
	User    string
	Key     string
	WorkDir string
}

// ParseRemote accepts ssh://user@host/path, user@host:/path, or host:/path.
func ParseRemote(raw string) (*RemoteSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty remote")
	}
	if strings.HasPrefix(raw, "wsl:") {
		rest := strings.TrimPrefix(raw, "wsl:")
		host, path, ok := strings.Cut(rest, ":")
		if !ok {
			host, path = rest, "~"
		}
		if host == "" {
			return nil, fmt.Errorf("wsl remote needs a distro name")
		}
		return &RemoteSpec{Kind: "wsl", Host: host, WorkDir: path}, nil
	}
	if strings.HasPrefix(raw, "docker:") {
		rest := strings.TrimPrefix(raw, "docker:")
		host, path, ok := strings.Cut(rest, ":")
		if !ok {
			host, path = rest, "/workspace"
		}
		if host == "" {
			return nil, fmt.Errorf("docker remote needs a container name")
		}
		return &RemoteSpec{Kind: "docker", Host: host, WorkDir: path}, nil
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse remote: %w", err)
		}
		if u.Scheme != "ssh" {
			return nil, fmt.Errorf("unsupported remote scheme %q", u.Scheme)
		}
		spec := &RemoteSpec{Kind: "ssh", Host: u.Hostname(), WorkDir: u.Path}
		if u.User != nil {
			spec.User = u.User.Username()
		}
		if spec.WorkDir == "" {
			spec.WorkDir = "."
		}
		return spec, nil
	}
	host, path, ok := strings.Cut(raw, ":")
	if !ok || host == "" || path == "" {
		return nil, fmt.Errorf("remote must look like user@host:/path or ssh://user@host/path")
	}
	spec := &RemoteSpec{Kind: "ssh", WorkDir: path}
	if u, h, found := strings.Cut(host, "@"); found {
		spec.User = u
		spec.Host = h
	} else {
		spec.Host = host
	}
	return spec, nil
}

func (s *RemoteSpec) Enabled() bool {
	if s == nil || s.Host == "" {
		return false
	}
	switch s.Kind {
	case "ssh", "wsl", "docker":
		return true
	default:
		return false
	}
}

func (s *RemoteSpec) SSHTarget() string {
	if s == nil {
		return ""
	}
	if s.User != "" {
		return s.User + "@" + s.Host
	}
	return s.Host
}
