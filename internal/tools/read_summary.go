package tools

import (
	"context"
	"encoding/json"

	"github.com/covoyage/covo-agent/internal/tools/capability"
	"github.com/covoyage/covonaut/agentcore"
)

type ReadSummary struct {
	Result         string `json:"result"`
	Complete       string `json:"complete"`
	Truncated      *bool  `json:"truncated,omitempty"`
	LimitReason    string `json:"limit_reason,omitempty"`
	Selection      string `json:"selection"`
	Offset         int    `json:"offset,omitempty"`
	RequestedLines *int   `json:"requested_lines,omitempty"`
	ObservedLines  int    `json:"observed_lines,omitempty"`
	ObservedBytes  int    `json:"observed_bytes,omitempty"`
	SourceRole     string `json:"source_role"`
}

func markToolWrapper(tool *agentcore.Tool, tag string) bool {
	for _, existing := range tool.Tags {
		if existing == tag {
			return false
		}
	}
	tool.Tags = append(tool.Tags, tag)
	return true
}

func ApplyReadSummaries(agent *agentcore.Agent) {
	for _, name := range agent.ToolNames() {
		tool, ok := agent.GetTool(name)
		if !ok || tool.Func == nil {
			continue
		}
		metadata, declared := capability.Lookup(tool)
		if !declared || metadata.Kind != capability.Read || metadata.ReadParam == "" {
			continue
		}
		if !markToolWrapper(tool, "covo-read-summary") {
			continue
		}
		original := tool.Func
		tool.Func = func(ctx context.Context, arguments json.RawMessage) (any, error) {
			result, err := original(ctx, arguments)
			if err != nil {
				return result, err
			}
			data, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return result, nil
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal(data, &envelope) != nil || envelope == nil {
				return result, nil
			}
			summary := summarizeRead(arguments, envelope["details"])
			var blocked bool
			_ = json.Unmarshal(envelope["blocked"], &blocked)
			if blocked {
				summary.Result = "blocked"
				summary.Complete = "no"
			}
			details := make(map[string]json.RawMessage)
			if len(envelope["details"]) > 0 && string(envelope["details"]) != "null" {
				if json.Unmarshal(envelope["details"], &details) != nil {
					return result, nil
				}
			}
			if details == nil {
				details = make(map[string]json.RawMessage)
			}
			details["source_summary"], _ = json.Marshal(summary)
			envelope["details"], _ = json.Marshal(details)
			return envelope, nil
		}
	}
}

func summarizeRead(arguments, details json.RawMessage) ReadSummary {
	var params struct {
		Offset *int `json:"offset"`
		Limit  *int `json:"limit"`
	}
	_ = json.Unmarshal(arguments, &params)
	summary := ReadSummary{Result: "succeeded", Complete: "unknown", Selection: "full", Offset: 1, SourceRole: "unknown"}
	if params.Offset != nil || params.Limit != nil {
		summary.Selection = "window"
	}
	if params.Offset != nil && *params.Offset > 0 {
		summary.Offset = *params.Offset
	}
	summary.RequestedLines = params.Limit
	var observed struct {
		Truncation *struct {
			Truncated   bool   `json:"truncated"`
			Reason      string `json:"truncated_by"`
			OutputLines int    `json:"output_lines"`
			OutputBytes int    `json:"output_bytes"`
		} `json:"truncation"`
	}
	if json.Unmarshal(details, &observed) != nil || observed.Truncation == nil {
		return summary
	}
	truncation := observed.Truncation
	summary.Truncated = &truncation.Truncated
	summary.LimitReason = truncation.Reason
	summary.ObservedLines, summary.ObservedBytes = truncation.OutputLines, truncation.OutputBytes
	if truncation.Truncated {
		summary.Complete = "no"
	} else if summary.Selection == "window" {
		summary.Complete = "window"
	} else {
		summary.Complete = "yes"
	}
	return summary
}

func ReadSummaryFromResult(result string) *ReadSummary {
	var envelope struct {
		Details struct {
			Source *ReadSummary `json:"source_summary"`
		} `json:"details"`
	}
	if json.Unmarshal([]byte(result), &envelope) != nil {
		return nil
	}
	return envelope.Details.Source
}
