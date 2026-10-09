package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildWebContextTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "web_context",
		Description: "Show bounded endpoint evidence and review status without raw HTTP bodies, headers, credentials, or query values. Use exchange_get for one explicitly chosen evidence ID.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Recorded HTTP exchange ID."},
		}, "required": []string{"id"}},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("cyber catalog unavailable")
			}
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			ex, found, err := catalog.GetExchange(params.ID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("exchange %q not found", params.ID)
			}
			origin, err := url.Parse(ex.URL)
			if err != nil || origin.Hostname() == "" {
				return nil, fmt.Errorf("recorded exchange URL is invalid")
			}
			all, err := catalog.ListExchanges()
			if err != nil {
				return nil, err
			}
			recent := make([]map[string]any, 0, 5)
			for index := len(all) - 1; index >= 0 && len(recent) < 5; index-- {
				item := all[index]
				parsed, parseErr := url.Parse(item.URL)
				if parseErr != nil || !sameHTTPOrigin(origin, parsed) || parsed.EscapedPath() != origin.EscapedPath() || item.Method != ex.Method {
					continue
				}
				recent = append(recent, map[string]any{
					"id": item.ID, "status": item.StatusCode, "identity": item.Identity,
					"role": item.Role, "tenant": item.Tenant, "resource": item.Resource,
					"baseline_id": item.BaselineID,
				})
			}
			findings, err := catalog.ListFindings()
			if err != nil {
				return nil, err
			}
			matchingFindings := make([]map[string]any, 0, 10)
			for _, finding := range findings {
				if len(matchingFindings) >= 10 {
					break
				}
				if finding.ExchangeID != ex.ID && (!strings.EqualFold(finding.Target, origin.Hostname()) ||
					(finding.Endpoint != "" && finding.Endpoint != origin.EscapedPath())) {
					continue
				}
				matchingFindings = append(matchingFindings, map[string]any{
					"id": finding.ID, "status": finding.Status, "severity": finding.Severity,
					"title": finding.Title, "duplicate_of": finding.DuplicateOf,
				})
			}
			coverage, err := catalog.ListCoverage()
			if err != nil {
				return nil, err
			}
			matchingCoverage := make([]map[string]any, 0, 10)
			for _, item := range coverage {
				if len(matchingCoverage) >= 10 {
					break
				}
				if !strings.EqualFold(item.Target, origin.Hostname()) && item.Target != origin.EscapedPath() {
					continue
				}
				matchingCoverage = append(matchingCoverage, map[string]any{
					"id": item.ID, "surface": item.Surface, "risk_class": item.RiskClass, "outcome": item.Outcome,
				})
			}
			jobs, err := catalog.IntakeJobs()
			if err != nil {
				return nil, err
			}
			pending := make([]map[string]any, 0, 5)
			endpoint := ex.Method + " " + origin.Scheme + "://" + origin.Host + origin.EscapedPath()
			for _, job := range jobs {
				if job.Endpoint == endpoint && len(pending) < 5 {
					pending = append(pending, map[string]any{"exchange_id": job.ExchangeID, "status": job.Status, "identity": job.Identity})
				}
			}
			return map[string]any{
				"request": map[string]any{"id": ex.ID, "method": ex.Method, "origin": origin.Scheme + "://" + origin.Host, "path": origin.EscapedPath()},
				"recent":  recent, "findings": matchingFindings, "coverage": matchingCoverage, "intake_jobs": pending,
			}, nil
		},
	}
}
