package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

type webParamProbeArgs struct {
	ID              string `json:"id"`
	Parameter       string `json:"parameter"`
	Location        string `json:"location"`
	Index           int    `json:"index"`
	Profile         string `json:"profile"`
	BaselineSamples *int   `json:"baseline_samples"`
	MaxRequests     *int   `json:"max_requests"`
}

func BuildWebParamProbeTool(catalog *cyber.Catalog, instanceRole string, transport http.RoundTripper) *agentcore.Tool {
	replay := BuildExchangeReplayTool(catalog, transport)
	return &agentcore.Tool{
		Name:        "web_param_probe",
		Description: "Probe one selected scalar. Default marker sends one inert marker: GET query, or an explicitly authorized form/JSON scalar in a complete POST/PUT/PATCH. Optional html (inert tag) or template (fixed arithmetic only) profiles repeat 3-5 baselines and make at most 12 replay attempts, GET query only. Return bounded observations and sampled length/timing noise, never vulnerability verdicts. Body probes may change application state. Automatic intake permits only the default single GET marker, without baseline/budget options.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id":               map[string]any{"type": "string", "description": "Recorded exchange ID."},
			"parameter":        map[string]any{"type": "string", "description": "Existing query/form name, or JSON Pointer such as /variables/items/0/id."},
			"location":         map[string]any{"type": "string", "enum": []string{"query", "form", "json"}, "description": "Default query. Body probes require the matching Content-Type."},
			"index":            map[string]any{"type": "integer", "minimum": 0, "description": "Query/form occurrence, zero-based; default 0. Not used for JSON."},
			"profile":          map[string]any{"type": "string", "enum": []string{"marker", "html", "template"}, "description": "Default marker. Classified profiles are GET query only; no scripts, commands, file access, or arbitrary payloads."},
			"baseline_samples": map[string]any{"type": "integer", "minimum": 3, "maximum": 5, "description": "Repeat the unchanged GET 3-5 times before probes; default 3 for classified profiles. Explicit use also enables repeated marker mode."},
			"max_requests":     map[string]any{"type": "integer", "minimum": 4, "maximum": 12, "description": "Total replay-attempt cap including baselines, default 8 in repeated mode. Must leave room for at least one probe."},
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
			var params webParamProbeArgs
			decoder := json.NewDecoder(strings.NewReader(string(args)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&params); err != nil {
				return nil, err
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return nil, fmt.Errorf("probe expects one JSON object")
			}
			if params.Location == "" {
				params.Location = "query"
			}
			if params.Profile == "" {
				params.Profile = "marker"
			}
			if params.Profile != "marker" && params.Profile != "html" && params.Profile != "template" {
				return nil, fmt.Errorf("invalid probe profile")
			}
			repeated := params.Profile != "marker" || params.BaselineSamples != nil || params.MaxRequests != nil
			if _, automatic := toolssubagent.IntakeScopeFromContext(ctx); automatic && (params.Location != "query" || repeated) {
				return nil, fmt.Errorf("automatic intake permits only single GET query marker probes")
			}
			source, found, err := catalog.GetExchange(params.ID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("recorded exchange not found")
			}
			parsed, err := url.Parse(source.URL)
			if err != nil {
				return nil, err
			}
			var nonce [8]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return nil, err
			}
			marker := "covo_probe_" + hex.EncodeToString(nonce[:])
			if repeated {
				if err := catalog.CheckReplayTarget(source.URL); err != nil {
					return nil, err
				}
				return runClassifiedProbe(ctx, replay, source, parsed, params, marker)
			}
			payloadArgs := map[string]any{"id": source.ID}
			switch params.Location {
			case "query":
				if source.Method != http.MethodGet {
					return nil, fmt.Errorf("query probe requires a recorded GET exchange")
				}
				parsed.RawQuery, err = probeValues(parsed.RawQuery, params.Parameter, params.Index, marker)
				payloadArgs["url"] = parsed.String()
			case "form", "json":
				if source.Method != http.MethodPost && source.Method != http.MethodPut && source.Method != http.MethodPatch {
					return nil, fmt.Errorf("body probe requires a recorded POST, PUT, or PATCH")
				}
				if source.Redacted || source.HeadersTruncated || source.RequestTruncated || strings.HasSuffix(source.Request, "\u2026") {
					return nil, fmt.Errorf("body probe requires complete unredacted request evidence")
				}
				headers := make(http.Header)
				applyRecordedHeaders(headers, source.ReqHeaders)
				contentType, _, typeErr := mime.ParseMediaType(headers.Get("Content-Type"))
				if typeErr != nil || (params.Location == "form" && contentType != "application/x-www-form-urlencoded") ||
					(params.Location == "json" && contentType != "application/json" && !strings.HasSuffix(contentType, "+json")) {
					return nil, fmt.Errorf("body probe requires a matching recorded Content-Type")
				}
				var body string
				if params.Location == "json" {
					if params.Index != 0 {
						return nil, fmt.Errorf("JSON probes use array indexes in the pointer, not index")
					}
					body, err = probeJSON(source.Request, params.Parameter, marker)
				} else {
					body, err = probeValues(source.Request, params.Parameter, params.Index, marker)
				}
				payloadArgs["body"] = body
			default:
				return nil, fmt.Errorf("invalid probe location")
			}
			if err != nil {
				return nil, err
			}
			payload, err := json.Marshal(payloadArgs)
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
				"location": params.Location, "index": params.Index,
				"status": row["status"], "observation": observation,
				"not_tested": []string{"script execution", "stored or DOM-based behavior", "other parameters", "authorization"},
			}, nil
		},
	}
}

type classifiedProbeCase struct {
	Name     string
	Value    string
	Expected string
}

func classifiedProbeCases(profile, marker string) []classifiedProbeCase {
	if profile == "html" {
		return []classifiedProbeCase{
			{Name: "marker", Value: marker},
			{Name: "html_tag", Value: `<covo-probe data-marker="` + marker + `">` + marker + `</covo-probe>`},
		}
	}
	if profile == "template" {
		digest := sha256.Sum256([]byte(marker))
		left, right := 1000+int(digest[0])*10+int(digest[1]), 1000+int(digest[2])*10+int(digest[3])
		expression := strconv.Itoa(left) + "*" + strconv.Itoa(right)
		expected := marker + "_" + strconv.Itoa(left*right) + "_" + marker
		return []classifiedProbeCase{
			{Name: "control", Value: marker + "_" + expression + "_" + marker, Expected: expected},
			{Name: "double_brace", Value: marker + "_{{" + expression + "}}_" + marker, Expected: expected},
			{Name: "dollar_brace", Value: marker + "_${" + expression + "}_" + marker, Expected: expected},
			{Name: "erb_expression", Value: marker + "_<%=" + expression + "%>_" + marker, Expected: expected},
		}
	}
	return []classifiedProbeCase{{Name: "marker", Value: marker}}
}

func summarizeProbeBaseline(rows []map[string]any) map[string]any {
	summary := map[string]any{"samples": []map[string]any{}, "status_stable": false, "body_stable": false, "complete": false}
	if len(rows) == 0 {
		return summary
	}
	status, hash := rows[0]["status"].(int), rows[0]["body_sha256"].(string)
	minLength, maxLength := rows[0]["response_length"].(int), rows[0]["response_length"].(int)
	minDuration, maxDuration := rows[0]["duration_ms"].(int64), rows[0]["duration_ms"].(int64)
	statusStable, bodyStable, complete := true, true, true
	samples := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		length, duration := row["response_length"].(int), row["duration_ms"].(int64)
		minLength, maxLength = min(minLength, length), max(maxLength, length)
		minDuration, maxDuration = min(minDuration, duration), max(maxDuration, duration)
		statusStable = statusStable && row["status"] == status
		bodyStable = bodyStable && row["body_sha256"] == hash
		complete = complete && row["body_truncated"] != true && row["evidence_clipped"] != true
		samples = append(samples, map[string]any{"exchange_id": row["exchange_id"], "status": row["status"], "response_length": length, "duration_ms": duration})
	}
	summary["samples"], summary["status"], summary["body_sha256"] = samples, status, hash
	summary["status_stable"], summary["body_stable"], summary["complete"] = statusStable, bodyStable, complete
	summary["response_length_min"], summary["response_length_max"] = minLength, maxLength
	summary["duration_ms_min"], summary["duration_ms_max"] = minDuration, maxDuration
	return summary
}

func probeSampleComparison(baseline map[string]any, row map[string]any) map[string]any {
	status := row["status"].(int)
	comparable := baseline["complete"] == true && baseline["status_stable"] == true &&
		row["body_truncated"] != true && row["evidence_clipped"] != true && status >= 200 && status < 300
	comparison := map[string]any{"comparable": comparable, "status_changed": row["status"] != baseline["status"]}
	if comparable {
		length := row["response_length"].(int)
		comparison["length_outside_baseline"] = length < baseline["response_length_min"].(int) || length > baseline["response_length_max"].(int)
		comparison["duration_above_baseline"] = row["duration_ms"].(int64) > baseline["duration_ms_max"].(int64)
		if baseline["body_stable"] == true {
			comparison["body_changed"] = row["body_sha256"] != baseline["body_sha256"]
		}
	}
	return comparison
}

func runClassifiedProbe(ctx context.Context, replay *agentcore.Tool, source cyber.Exchange, parsed *url.URL, params webParamProbeArgs, marker string) (map[string]any, error) {
	if source.Method != http.MethodGet || params.Location != "query" {
		return nil, fmt.Errorf("classified or repeated probes require a GET query; body requests are never repeated")
	}
	if source.Request != "" {
		return nil, fmt.Errorf("classified probes require a GET with no recorded request body")
	}
	if source.Redacted || source.HeadersTruncated || source.RequestTruncated || strings.HasSuffix(source.ReqHeaders, "\u2026") || strings.HasSuffix(source.Request, "\u2026") {
		return nil, fmt.Errorf("classified probes require complete unredacted request evidence")
	}
	if _, err := probeValues(parsed.RawQuery, params.Parameter, params.Index, marker); err != nil {
		return nil, err
	}
	baselineSamples, maxRequests := 3, 8
	if params.BaselineSamples != nil {
		baselineSamples = *params.BaselineSamples
	}
	if params.MaxRequests != nil {
		maxRequests = *params.MaxRequests
	}
	if baselineSamples < 3 || baselineSamples > 5 || maxRequests > 12 || maxRequests < baselineSamples+1 {
		return nil, fmt.Errorf("baseline_samples must be 3-5; max_requests must be %d-12", baselineSamples+1)
	}
	cases := classifiedProbeCases(params.Profile, marker)
	baselines := make([]map[string]any, 0, baselineSamples)
	observations := make([]map[string]any, 0, len(cases))
	attempts, stopReason := 0, ""
	send := func(value *string) (map[string]any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		args := map[string]any{"id": source.ID}
		if value != nil {
			target := *parsed
			var err error
			target.RawQuery, err = probeValues(parsed.RawQuery, params.Parameter, params.Index, *value)
			if err != nil {
				return nil, err
			}
			args["url"] = target.String()
		}
		payload, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		attempts++
		result, err := replay.Func(ctx, payload)
		if err != nil {
			return nil, err
		}
		return result.(map[string]any), nil
	}
	for len(baselines) < baselineSamples {
		row, err := send(nil)
		if err != nil {
			stopReason = "replay_failed"
			break
		}
		baselines = append(baselines, row)
		if row["body_truncated"] == true || row["evidence_clipped"] == true {
			stopReason = "inconclusive_baseline_truncated"
			break
		}
		if status := row["status"].(int); status < 200 || status >= 300 {
			stopReason = "inconclusive_baseline_status"
			break
		}
		if row["status"] != baselines[0]["status"] {
			stopReason = "inconclusive_baseline_status_changes"
			break
		}
	}
	baseline := summarizeProbeBaseline(baselines)
	baseline["complete"] = baseline["complete"] == true && len(baselines) == baselineSamples
	baseline["requested_samples"] = baselineSamples
	if stopReason == "" {
		for _, probe := range cases {
			if attempts >= maxRequests {
				stopReason = "request_limit"
				break
			}
			row, err := send(&probe.Value)
			if err != nil {
				stopReason = "replay_failed"
				break
			}
			response := row["response"].(string)
			observation := "not_observed"
			comparison := probeSampleComparison(baseline, row)
			switch {
			case row["body_truncated"] == true || row["evidence_clipped"] == true:
				observation = "inconclusive_truncated"
			case comparison["comparable"] != true:
				observation = "inconclusive_status"
			case probe.Expected != "" && strings.Contains(response, probe.Expected):
				observation = "arithmetic_evaluation_observed"
				if probe.Name == "control" || strings.Contains(response, probe.Value) {
					observation = "inconclusive_control"
				}
				for _, base := range baselines {
					if strings.Contains(base["response"].(string), probe.Expected) {
						observation = "inconclusive_control"
					}
				}
			case probe.Name == "html_tag" && strings.Contains(response, probe.Value):
				observation = "raw_html_observed"
			case probe.Name == "html_tag" && strings.Contains(html.UnescapeString(response), probe.Value):
				observation = "html_encoded"
			case strings.Contains(response, marker):
				observation = "marker_observed"
			}
			observations = append(observations, map[string]any{"probe": probe.Name, "exchange_id": row["exchange_id"], "status": row["status"], "observation": observation, "comparison": comparison})
			if strings.HasPrefix(observation, "inconclusive_") {
				stopReason = observation
				break
			}
		}
	}
	if ctx.Err() != nil {
		stopReason = "cancelled"
	}
	untested := []string{"script execution", "stored or DOM-based behavior", "other parameters", "authorization", "SQL injection", "command injection", "file access", "blind or out-of-band behavior", "statistically significant timing differences"}
	if params.Profile != "html" {
		untested = append(untested, "HTML tag handling")
	}
	if params.Profile != "template" {
		untested = append(untested, "template evaluation")
	}
	for _, probe := range cases[len(observations):] {
		untested = append(untested, "probe:"+probe.Name)
	}
	return map[string]any{
		"baseline_id": source.ID, "parameter": params.Parameter, "location": params.Location, "index": params.Index, "profile": params.Profile,
		"baseline": baseline, "observations": observations, "replay_attempts": attempts, "max_requests": maxRequests,
		"complete": stopReason == "" && len(observations) == len(cases), "stop_reason": stopReason, "not_tested": untested,
		"interpretation": "Observations only. Complete means this bounded probe plan finished, not that the vulnerability class was fully tested. Sampled length and timing differences are not statistically significant proof.",
	}, ctx.Err()
}

func probeValues(raw, parameter string, index int, marker string) (string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || parameter == "" || index < 0 || index >= len(values[parameter]) {
		return "", fmt.Errorf("parameter %q occurrence %d is missing or invalid", parameter, index)
	}
	parts := strings.Split(raw, "&")
	occurrence := 0
	for position, part := range parts {
		encodedName, _, _ := strings.Cut(part, "=")
		name, err := url.QueryUnescape(encodedName)
		if err != nil {
			return "", err
		}
		if name != parameter {
			continue
		}
		if occurrence == index {
			parts[position] = encodedName + "=" + url.QueryEscape(marker)
			return strings.Join(parts, "&"), nil
		}
		occurrence++
	}
	return "", fmt.Errorf("parameter occurrence not found")
}

func probeJSON(raw, pointer, marker string) (string, error) {
	if len(pointer) > 512 || !strings.HasPrefix(pointer, "/") || !json.Valid([]byte(raw)) {
		return "", fmt.Errorf("a valid JSON body and non-root JSON Pointer are required")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := validateProbeJSON(decoder, 0, &nodes); err != nil {
		return "", err
	}
	decoder = json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var body any
	if err := decoder.Decode(&body); err != nil {
		return "", err
	}
	segments := strings.Split(pointer[1:], "/")
	if len(segments) > 8 {
		return "", fmt.Errorf("JSON Pointer exceeds 8 levels")
	}
	current := body
	for index, segment := range segments {
		for offset := 0; offset < len(segment); offset++ {
			if segment[offset] == '~' {
				if offset+1 == len(segment) || (segment[offset+1] != '0' && segment[offset+1] != '1') {
					return "", fmt.Errorf("invalid JSON Pointer escape")
				}
				offset++
			}
		}
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		last := index == len(segments)-1
		var child any
		switch node := current.(type) {
		case map[string]any:
			var exists bool
			child, exists = node[segment]
			if !exists {
				return "", fmt.Errorf("JSON Pointer field does not exist")
			}
			if last {
				switch child.(type) {
				case map[string]any, []any:
					return "", fmt.Errorf("JSON probe requires a scalar field")
				}
				node[segment] = marker
			}
		case []any:
			position, err := strconv.Atoi(segment)
			if err != nil || position < 0 || position >= len(node) || strconv.Itoa(position) != segment {
				return "", fmt.Errorf("invalid JSON Pointer array index")
			}
			child = node[position]
			if last {
				switch child.(type) {
				case map[string]any, []any:
					return "", fmt.Errorf("JSON probe requires a scalar field")
				}
				node[position] = marker
			}
		default:
			return "", fmt.Errorf("JSON Pointer traverses a scalar")
		}
		current = child
	}
	encoded, err := json.Marshal(body)
	return string(encoded), err
}

func validateProbeJSON(decoder *json.Decoder, depth int, nodes *int) error {
	*nodes += 1
	if depth > 8 || *nodes > 128 {
		return fmt.Errorf("JSON probe body exceeds 8 levels or 128 values")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, valid := key.(string)
			if !valid || seen[name] {
				return fmt.Errorf("JSON probe body contains duplicate object keys")
			}
			seen[name] = true
		}
		if err := validateProbeJSON(decoder, depth+1, nodes); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
