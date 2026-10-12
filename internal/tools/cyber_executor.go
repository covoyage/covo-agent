package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildWebTestExecuteTool(catalog *cyber.Catalog, instanceRole string, transport http.RoundTripper) *agentcore.Tool {
	replay := BuildExchangeReplayTool(catalog, transport)
	return &agentcore.Tool{
		Name:        "web_test_execute",
		Description: "Execute a bounded concurrency comparison or one fixed text-file upload against complete chosen evidence. Requires acknowledge_state_change=true and a spawned hands-on role. concurrency sends two serial baselines then 2-4 concurrent requests (maximum 6 sends); upload replaces one unique existing multipart file with a nonce text marker, no executable payload. Scope, approval and daily budgets still apply. Returns evidence IDs and response facts, not race/upload vulnerability verdicts. No scripts, arbitrary payloads, auth replacement or automatic intake.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string", "enum": []string{"concurrency", "upload"}},
			"acknowledge_state_change": map[string]any{"type": "boolean"}, "concurrency": map[string]any{"type": "integer", "minimum": 2, "maximum": 4},
			"file_field": map[string]any{"type": "string", "description": "Unique existing multipart file field for upload."},
		}, "required": []string{"id", "kind", "acknowledge_state_change"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil || transport == nil || !cyber.AllowAgentTool(instanceRole, subagentRole(ctx), "web_test_execute") {
				return nil, fmt.Errorf("web_test_execute requires safe transport and a spawned hands-on role")
			}
			if _, automatic := toolssubagent.IntakeScopeFromContext(ctx); automatic {
				return nil, fmt.Errorf("automatic intake cannot execute specialized tests")
			}
			var params struct {
				ID          string `json:"id"`
				Kind        string `json:"kind"`
				Acknowledge bool   `json:"acknowledge_state_change"`
				Concurrency int    `json:"concurrency"`
				FileField   string `json:"file_field"`
			}
			if err := decodeCyberValidationArgs(args, &params); err != nil {
				return nil, err
			}
			if !params.Acknowledge {
				return nil, fmt.Errorf("acknowledge_state_change=true is required")
			}
			source, found, err := catalog.GetExchange(params.ID)
			if err != nil || !found {
				return nil, fmt.Errorf("selected exchange unavailable")
			}
			if source.Redacted || source.RequestTruncated || source.HeadersTruncated || strings.HasSuffix(source.Request, "\u2026") || strings.HasSuffix(source.ReqHeaders, "\u2026") {
				return nil, fmt.Errorf("specialized tests require complete unredacted requests")
			}
			if err := catalog.CheckReplayTarget(source.URL); err != nil {
				return nil, err
			}
			if instanceRole != "" {
				ctx = toolssubagent.WithSubagentRole(ctx, instanceRole)
			}
			if source.Method != "GET" && source.Method != "POST" && source.Method != "PUT" && source.Method != "PATCH" {
				return nil, fmt.Errorf("unsupported test method")
			}
			if source.Method == "GET" && source.Request != "" {
				return nil, fmt.Errorf("GET request bodies are unsupported")
			}
			if params.Kind == "upload" {
				if params.Concurrency != 0 || source.Method != "POST" {
					return nil, fmt.Errorf("upload requires POST and no concurrency option")
				}
				body, contentType, marker, err := fixedTextUpload(source, params.FileField)
				if err != nil {
					return nil, err
				}
				payload, _ := json.Marshal(map[string]any{"id": source.ID, "body": body, "headers": map[string]string{"Content-Type": contentType}})
				result, err := replay.Func(ctx, payload)
				if err != nil {
					return nil, err
				}
				row := result.(map[string]any)
				return map[string]any{"kind": "upload", "baseline_id": source.ID, "exchange_id": row["exchange_id"], "status": row["status"], "response_complete": row["body_truncated"] != true && row["evidence_clipped"] != true, "marker_reflected": strings.Contains(row["response"].(string), marker), "conclusion": "undetermined; response acceptance or marker reflection does not prove unsafe upload handling"}, nil
			}
			if params.Kind != "concurrency" || params.FileField != "" {
				return nil, fmt.Errorf("select concurrency or upload with matching options")
			}
			count := params.Concurrency
			if count == 0 {
				count = 2
			}
			if count < 2 || count > 4 {
				return nil, fmt.Errorf("concurrency must be 2-4")
			}
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			payload, _ := json.Marshal(map[string]string{"id": source.ID})
			baselines := []map[string]any{}
			baselineSummary := func() map[string]any {
				summary := summarizeProbeBaseline(baselines)
				summary["requested_samples"] = 2
				summary["complete"] = summary["complete"] == true && len(baselines) == 2
				return summary
			}
			for sample := 0; sample < 2; sample++ {
				result, err := replay.Func(ctx, payload)
				if err != nil {
					return map[string]any{"baseline": baselineSummary(), "complete": false}, err
				}
				row := result.(map[string]any)
				baselines = append(baselines, row)
				if row["body_truncated"] == true || row["evidence_clipped"] == true || row["status"].(int) < 200 || row["status"].(int) >= 300 {
					return map[string]any{"baseline": baselineSummary(), "complete": false, "stop_reason": "baseline_inconclusive"}, nil
				}
			}
			baseline := baselineSummary()
			if baseline["status_stable"] != true {
				return map[string]any{"baseline": baseline, "complete": false, "stop_reason": "baseline_status_changes"}, nil
			}
			results := make([]map[string]any, count)
			failures := make([]error, count)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for position := 0; position < count; position++ {
				workers.Add(1)
				go func(position int) {
					defer workers.Done()
					select {
					case <-ctx.Done():
						failures[position] = ctx.Err()
						return
					case <-start:
					}
					result, err := replay.Func(ctx, payload)
					if err != nil {
						failures[position] = err
						cancel()
						return
					}
					results[position] = result.(map[string]any)
				}(position)
			}
			close(start)
			workers.Wait()
			observations := []map[string]any{}
			complete := true
			for position, row := range results {
				if failures[position] != nil {
					complete = false
					observations = append(observations, map[string]any{"attempt": position, "status": "failed"})
					continue
				}
				comparison := probeSampleComparison(baseline, row)
				complete = complete && comparison["comparable"] == true
				observations = append(observations, map[string]any{"attempt": position, "exchange_id": row["exchange_id"], "status": row["status"], "comparison": comparison})
			}
			return map[string]any{"kind": "concurrency", "baseline_id": source.ID, "baseline": baseline, "observations": observations, "complete": complete, "conclusion": "undetermined; overlap is best effort, response differences are not a race verdict; independently inspect business state and negative controls"}, ctx.Err()
		},
	}
}

func fixedTextUpload(source cyber.Exchange, field string) (string, string, string, error) {
	if field == "" || len(field) > 128 {
		return "", "", "", fmt.Errorf("a bounded existing file field is required")
	}
	headers := make(http.Header)
	applyRecordedHeaders(headers, source.ReqHeaders)
	mediaType, params, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return "", "", "", fmt.Errorf("complete multipart/form-data source is required")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", "", err
	}
	marker := "covo_upload_" + hex.EncodeToString(nonce[:])
	reader := multipart.NewReader(strings.NewReader(source.Request), params["boundary"])
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	matches, parts := 0, 0
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", "", fmt.Errorf("invalid multipart source")
		}
		parts++
		if parts > 16 {
			return "", "", "", fmt.Errorf("multipart limited to 16 parts")
		}
		data, err := io.ReadAll(io.LimitReader(part, 4001))
		if err != nil || len(data) > 4000 {
			return "", "", "", fmt.Errorf("multipart part exceeds budget")
		}
		partHeaders := textproto.MIMEHeader{}
		for name, values := range part.Header {
			partHeaders[name] = append([]string(nil), values...)
		}
		if part.FormName() == field {
			if part.FileName() == "" {
				return "", "", "", fmt.Errorf("selected part is not a file")
			}
			matches++
			partHeaders.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": "covo-probe.txt"}))
			partHeaders.Set("Content-Type", "text/plain")
			partHeaders.Del("Content-Transfer-Encoding")
			data = []byte(marker + "\n")
		}
		destination, err := writer.CreatePart(partHeaders)
		if err != nil {
			return "", "", "", err
		}
		if _, err := destination.Write(data); err != nil {
			return "", "", "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", "", "", err
	}
	if matches != 1 || body.Len() > 4000 {
		return "", "", "", fmt.Errorf("exactly one selected file and a body within 4000 bytes are required")
	}
	return body.String(), writer.FormDataContentType(), marker, nil
}
