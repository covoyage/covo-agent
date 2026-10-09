package cyber

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type RemoteNode struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	User    string `json:"user"`
	KeyPath string `json:"key_path,omitempty"`
	Zone    string `json:"zone,omitempty"`
}

type NodeResult struct {
	Node     string `json:"node"`
	Zone     string `json:"zone,omitempty"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

var nodeToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func (c *Catalog) nodesPath() string { return filepath.Join(c.dir, "nodes.json") }

func (c *Catalog) ListNodes() ([]RemoteNode, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var nodes []RemoteNode
	return nodes, c.readJSON(c.nodesPath(), &nodes)
}

func (c *Catalog) UpsertNode(node RemoteNode) (RemoteNode, error) {
	node.ID, node.Host, node.User = strings.TrimSpace(node.ID), strings.TrimSpace(node.Host), strings.TrimSpace(node.User)
	if !nodeToken.MatchString(node.ID) || !nodeToken.MatchString(node.Host) || !nodeToken.MatchString(node.User) {
		return RemoteNode{}, fmt.Errorf("node id, host, and user must be nonempty plain DNS/IP or account names")
	}
	if node.KeyPath != "" && (!filepath.IsAbs(node.KeyPath) || strings.ContainsAny(node.KeyPath, "\r\n")) {
		return RemoteNode{}, fmt.Errorf("key path must be an absolute file path")
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var nodes []RemoteNode
	if err := c.readJSON(c.nodesPath(), &nodes); err != nil {
		return RemoteNode{}, err
	}
	replaced := false
	for i := range nodes {
		if nodes[i].ID == node.ID {
			nodes[i] = node
			replaced = true
		}
	}
	if !replaced {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return node, c.writeJSON(c.nodesPath(), nodes)
}

func (c *Catalog) RemoveNode(id string) error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var nodes []RemoteNode
	if err := c.readJSON(c.nodesPath(), &nodes); err != nil {
		return err
	}
	for i, node := range nodes {
		if node.ID == id {
			nodes = append(nodes[:i], nodes[i+1:]...)
			return c.writeJSON(c.nodesPath(), nodes)
		}
	}
	return fmt.Errorf("node %q not found", id)
}

func (c *Catalog) ExecuteNode(ctx context.Context, id, command string) (NodeResult, error) {
	if strings.TrimSpace(command) == "" {
		return NodeResult{}, fmt.Errorf("command is required")
	}
	nodes, err := c.ListNodes()
	if err != nil {
		return NodeResult{}, err
	}
	for _, node := range nodes {
		if node.ID != id {
			continue
		}
		if ctx == nil {
			ctx = context.Background()
		}
		runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10"}
		if node.KeyPath != "" {
			args = append(args, "-i", node.KeyPath)
		}
		args = append(args, node.User+"@"+node.Host, command)
		out, err := exec.CommandContext(runCtx, "ssh", args...).CombinedOutput()
		result := NodeResult{Node: node.ID, Zone: node.Zone, Output: clip(string(out), 12000)}
		if err == nil {
			return result, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	return NodeResult{}, fmt.Errorf("node %q is not registered", id)
}
