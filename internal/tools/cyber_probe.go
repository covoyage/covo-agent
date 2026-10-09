package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildWebParamProbeTool(catalog *cyber.Catalog, instanceRole string, transport http.RoundTripper) *agentcore.Tool {
	replay := BuildExchangeReplayTool(catalog, transport)
	return &agentcore.Tool{
		Name:        "web_param_probe",
		Description: "Send one inert marker in an existing query parameter of a recorded GET request. Return reflection observations and evidence, never a vulnerability verdict. Does not test script execution, other parameters, or stored/DOM behavior.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id":        map[string]any{"type": "string", "description": "Recorded GET exchange ID."},
			"parameter": map[string]any{"type": "string", "description": "Existing query parameter to check."},
		}, "required": []string{"id", "parameter"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil || transport == nil {
				return nil, fmt.Errorf("cyber catalog and safe HTTP transport are required")
			}
			if !cyber.AllowAgentTool(instanceRole, subagentRole(ctx), "web_param_probe") {
				return nil, fmt.Errorf("web_param_probe is reserved for spawned hands-on roles")
			}
			if instanceRole != "" {
				ctx = toolssubagent.WithSubagentRole(ctx, instanceRole)
			}
			var params struct {
				ID        string `json:"id"`
				Parameter string `json:"parameter"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			source, found, err := catalog.GetExchange(params.ID)
			if err != nil {
				return nil, err
			}
			if !found || source.Method != http.MethodGet {
				return nil, fmt.Errorf("probe requires a recorded GET exchange")
			}
			parsed, err := url.Parse(source.URL)
			if err != nil {
				return nil, err
			}
			values, err := url.ParseQuery(parsed.RawQuery)
			if err != nil || strings.TrimSpace(params.Parameter) == "" || !values.Has(params.Parameter) {
				return nil, fmt.Errorf("query parameter %q is missing or invalid", params.Parameter)
			}
			var nonce [8]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return nil, err
			}
			marker := "covo_probe_" + hex.EncodeToString(nonce[:])
			values.Set(params.Parameter, marker)
			parsed.RawQuery = values.Encode()
			payload, err := json.Marshal(map[string]string{"id": source.ID, "url": parsed.String()})
			if err != nil {
				return nil, err
			}
			result, err := replay.Func(ctx, payload)
			if err != nil {
				return nil, err
			}
			row := result.(map[string]any)
			response := row["response"].(string)
			reflected := strings.Contains(response, marker)
			observation := "not_observed"
			if reflected {
				observation = "marker_observed"
			} else if row["body_truncated"] == true || row["evidence_clipped"] == true {
				observation = "inconclusive_truncated"
			}
			return map[string]any{
				"exchange_id": row["exchange_id"], "baseline_id": source.ID, "parameter": params.Parameter,
				"status": row["status"], "observation": observation,
				"not_tested": []string{"script execution", "stored or DOM-based behavior", "other parameters", "authorization"},
			}, nil
		},
	}
}
