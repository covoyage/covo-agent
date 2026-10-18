package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

type Kind string

const (
	Read        Kind = "read"
	Write       Kind = "write"
	Execute     Kind = "execute"
	Conditional Kind = "conditional"
)

type Metadata struct {
	Kind        Kind
	WriteParams []string
	ReadParam   string
	Patch       bool
}

const prefix = "covo-capability:"

func Declare(tool *agentcore.Tool, metadata Metadata) {
	filtered := make([]string, 0, len(tool.Tags)+1)
	for _, tag := range tool.Tags {
		if !strings.HasPrefix(tag, prefix) {
			filtered = append(filtered, tag)
		}
	}
	data, _ := json.Marshal(metadata)
	tool.Tags = append(filtered, prefix+string(data))
	if metadata.Kind == Write || metadata.Kind == Execute || metadata.Kind == Conditional {
		unsafe := false
		tool.ConcurrencySafe = &unsafe
	}
}

func Lookup(tool *agentcore.Tool) (Metadata, bool) {
	if tool == nil {
		return Metadata{}, false
	}
	for _, tag := range tool.Tags {
		if strings.HasPrefix(tag, prefix) {
			var metadata Metadata
			if json.Unmarshal([]byte(strings.TrimPrefix(tag, prefix)), &metadata) != nil {
				return Metadata{Kind: Execute}, true
			}
			return metadata, true
		}
	}
	return Metadata{}, false
}

func (metadata Metadata) Paths(arguments json.RawMessage) ([]string, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &args); err != nil {
		return nil, err
	}
	var paths []string
	for _, param := range metadata.WriteParams {
		value, exists := args[param]
		if !exists {
			return nil, fmt.Errorf("missing mutation target %q", param)
		}
		var path string
		if json.Unmarshal(value, &path) == nil && path != "" {
			paths = append(paths, path)
			continue
		}
		var targets []string
		if json.Unmarshal(value, &targets) != nil || len(targets) == 0 {
			return nil, fmt.Errorf("invalid mutation target %q", param)
		}
		for _, target := range targets {
			if target == "" {
				return nil, fmt.Errorf("empty mutation target")
			}
			paths = append(paths, target)
		}
	}
	return paths, nil
}

type contextKey struct{}

func WithMetadata(ctx context.Context, metadata Metadata) context.Context {
	return context.WithValue(ctx, contextKey{}, metadata)
}

func FromContext(ctx context.Context) (Metadata, bool) {
	metadata, ok := ctx.Value(contextKey{}).(Metadata)
	return metadata, ok
}

func DeclareBuiltin(tool *agentcore.Tool) {
	var metadata Metadata
	switch tool.Name {
	case "write_file", "write", "edit", "edit_block", "patch", "delete":
		metadata = Metadata{Kind: Write, WriteParams: []string{"path"}}
	case "append_file":
		metadata = Metadata{Kind: Write, WriteParams: []string{"file_path"}}
	case "move":
		metadata = Metadata{Kind: Write, WriteParams: []string{"source", "dest"}}
	case "apply_patch":
		metadata = Metadata{Kind: Write, Patch: true}
	case "read", "read_file":
		metadata = Metadata{Kind: Read, ReadParam: "path"}
	case "audit_read":
		metadata = Metadata{Kind: Read, ReadParam: "file_path"}
	case "bash", "process", "sandbox", "remote_exec", "external_agent", "execute_code", "skill_script", "preview", "computer_use", "deploy":
		metadata = Metadata{Kind: Execute}
	case "mcp", "scan", "cronjob", "scan_profile", "request_intake", "browser":
		metadata = Metadata{Kind: Conditional}
	default:
		return
	}
	Declare(tool, metadata)
}
