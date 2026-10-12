package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func decodeCyberValidationArgs(args json.RawMessage, target any) error {
	if len(args) > 32<<10 || !strings.HasPrefix(strings.TrimSpace(string(args)), "{") {
		return fmt.Errorf("one bounded JSON object is required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(args)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("one JSON object is required")
	}
	return nil
}

func BuildWebAccessMatrixTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "web_access_matrix",
		Description: "Build a read-only permission test plan from at most 12 chosen exchanges and explicit identity/allow/deny rules. Observed identity, role, tenant, resource and owner labels are unverified claims. Missing rules stay unknown. Returns operation/field names and evidence IDs, never credentials, raw bodies, or automatic permission conclusions. Sends no requests.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"exchange_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": map[string]any{"type": "string"}},
			"rules": map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "object", "properties": map[string]any{
				"identity": map[string]any{"type": "string"}, "exchange_id": map[string]any{"type": "string"}, "expected": map[string]any{"type": "string", "enum": []string{"allow", "deny"}},
			}, "required": []string{"identity", "exchange_id", "expected"}}},
		}, "required": []string{"exchange_ids"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("cyber catalog is required")
			}
			if _, automatic := toolssubagent.IntakeScopeFromContext(ctx); automatic {
				return nil, fmt.Errorf("automatic intake cannot build cross-identity plans")
			}
			var params struct {
				ExchangeIDs []string `json:"exchange_ids"`
				Rules       []struct {
					Identity   string `json:"identity"`
					ExchangeID string `json:"exchange_id"`
					Expected   string `json:"expected"`
				} `json:"rules"`
			}
			if err := decodeCyberValidationArgs(args, &params); err != nil {
				return nil, err
			}
			if len(params.ExchangeIDs) == 0 || len(params.ExchangeIDs) > 12 || len(params.Rules) > 100 {
				return nil, fmt.Errorf("select 1-12 exchanges and at most 100 rules")
			}
			sources, identities := map[string]cyber.Exchange{}, map[string]bool{}
			operations := []map[string]any{}
			complete := true
			for _, id := range params.ExchangeIDs {
				if _, duplicate := sources[id]; duplicate {
					return nil, fmt.Errorf("duplicate exchange ID")
				}
				ex, found, err := catalog.GetExchange(id)
				if err != nil || !found {
					return nil, fmt.Errorf("selected exchange is unavailable: %s", id)
				}
				operation, err := catalog.RequestContext(id)
				if err != nil {
					return nil, err
				}
				parsed, err := url.Parse(ex.URL)
				if err != nil || parsed.User != nil {
					return nil, fmt.Errorf("invalid source URL")
				}
				sources[id] = ex
				if ex.Identity != "" {
					identities[ex.Identity] = true
				}
				fields, fieldsComplete := accessMatrixFields(ex.Request)
				responseFields, responseComplete := accessMatrixFields(ex.Response)
				entryComplete := operation.Shape.Complete && fieldsComplete && responseComplete && !ex.ResponseTruncated && !ex.Redacted
				complete = complete && entryComplete
				action := "read"
				if ex.Method != "GET" && ex.Method != "HEAD" {
					action = "write"
				}
				operations = append(operations, map[string]any{
					"exchange_id": id, "operation_key": operation.Shape.Key, "method": ex.Method, "action_type": action,
					"observed_identity": ex.Identity, "observed_role": ex.Role, "observed_tenant": ex.Tenant,
					"observed_resource": ex.Resource, "observed_owner": ex.Owner,
					"request_fields": fields, "response_fields": responseFields,
					"complete": entryComplete,
				})
			}
			rules := map[string]map[string]string{}
			for _, rule := range params.Rules {
				if strings.TrimSpace(rule.Identity) == "" || len(rule.Identity) > 128 || (rule.Expected != "allow" && rule.Expected != "deny") {
					return nil, fmt.Errorf("each rule needs a bounded identity and allow/deny expectation")
				}
				if _, found := sources[rule.ExchangeID]; !found {
					return nil, fmt.Errorf("rule references an unselected exchange")
				}
				if rules[rule.Identity] == nil {
					rules[rule.Identity] = map[string]string{}
				}
				if _, duplicate := rules[rule.Identity][rule.ExchangeID]; duplicate {
					return nil, fmt.Errorf("duplicate or conflicting permission rule")
				}
				rules[rule.Identity][rule.ExchangeID] = rule.Expected
				identities[rule.Identity] = true
			}
			if len(identities) == 0 || len(identities)*len(sources) > 144 {
				return nil, fmt.Errorf("explicit identity labels required; matrix is capped at 144 cases")
			}
			names := make([]string, 0, len(identities))
			for name := range identities {
				names = append(names, name)
			}
			sort.Strings(names)
			cases := []map[string]any{}
			for _, identity := range names {
				for _, id := range params.ExchangeIDs {
					expected := rules[identity][id]
					if expected == "" {
						expected = "unknown"
					}
					cases = append(cases, map[string]any{"identity": identity, "source_exchange_id": id, "expected": expected, "status": "untested", "next_tool": "exchange_compare", "checks": []string{"object_access", "function_access"}})
				}
			}
			return map[string]any{"operations": operations, "cases": cases, "complete": complete,
				"interpretation": "Labels and rules are operator claims, not proof. Verify test credentials independently before executing. Unknown expectations require review; no requests were sent."}, nil
		},
	}
}

func accessMatrixFields(raw string) ([]string, bool) {
	fields := []string{}
	if raw == "" {
		return fields, true
	}
	if !json.Valid([]byte(raw)) {
		return fields, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	nodes := 0
	if err := validateProbeJSON(decoder, 0, &nodes); err != nil {
		return fields, false
	}
	decoder = json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fields, false
	}
	var walk func(any, string)
	walk = func(value any, prefix string) {
		switch item := value.(type) {
		case map[string]any:
			for name, child := range item {
				if sensitiveMatrixField(name) {
					continue
				}
				pointer := prefix + "/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
				fields = append(fields, pointer)
				walk(child, pointer)
			}
		case []any:
			for _, child := range item {
				walk(child, prefix+"/*")
			}
		}
	}
	walk(value, "")
	sort.Strings(fields)
	fields = compactMatrixFields(fields)
	complete := len(fields) <= 32
	if !complete {
		fields = fields[:32]
	}
	return fields, complete
}

func sensitiveMatrixField(name string) bool {
	for _, fragment := range []string{"auth", "token", "secret", "password", "passwd", "cookie", "session", "credential", "csrf", "email", "phone"} {
		if strings.Contains(strings.ToLower(name), fragment) {
			return true
		}
	}
	return false
}

func compactMatrixFields(fields []string) []string {
	result := []string{}
	for _, field := range fields {
		if len(result) == 0 || result[len(result)-1] != field {
			result = append(result, field)
		}
	}
	return result
}
