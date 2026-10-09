package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	internalsandbox "github.com/covoyage/covo-agent/internal/sandbox"
	"github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
	"github.com/covoyage/covonaut/agentcore"
)

func (e *Extension) findingCatalog() *cyber.Catalog {
	if e.catalog == nil {
		e.catalog = cyber.NewCatalog(e.workDir)
	}
	return e.catalog
}

func BuildFindingUpsertTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "finding_upsert",
		Description: strings.Join([]string{
			"Record or update a security finding. Use after recon or verification produces evidence.",
			"Related findings remain separate until finding_triage approves them or links a duplicate.",
			"Omit title/target to list current findings.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "string", "description": "Stable id; omit to generate."},
				"title":       map[string]any{"type": "string"},
				"severity":    map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low", "info"}},
				"target":      map[string]any{"type": "string"},
				"evidence":    map[string]any{"type": "string"},
				"status":      map[string]any{"type": "string", "description": "open, open_proof_gap, confirmed, ruled_out, or false-positive. confirmed and ruled_out require poc or exchange_id. verified aliases confirmed."},
				"poc":         map[string]any{"type": "string", "description": "Reproduction command, request, or script."},
				"exchange_id": map[string]any{"type": "string", "description": "Recorded HTTP exchange id (ex-N). Counts as proof."},
				"root_cause":  map[string]any{"type": "string", "description": "Technical root cause used for semantic deduplication."},
				"endpoint":    map[string]any{"type": "string"},
				"method":      map[string]any{"type": "string"},
				"parameter":   map[string]any{"type": "string"},
				"cwe":         map[string]any{"type": "string"},
				"cve":         map[string]any{"type": "string"},
				"cvss":        map[string]any{"type": "number"},
				"file":        map[string]any{"type": "string", "description": "Repository-relative source path."},
				"line":        map[string]any{"type": "integer", "minimum": 1},
				"remediation": map[string]any{"type": "string"},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if findingArgsEmpty(args) {
				items, err := catalog.ListFindings()
				if err != nil {
					return nil, err
				}
				return map[string]any{"findings": items, "count": len(items)}, nil
			}
			var f cyber.Finding
			if err := json.Unmarshal(args, &f); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			saved, err := catalog.UpsertFinding(f)
			if err != nil {
				return nil, err
			}
			return saved, nil
		},
	}
}

func BuildFindingTriageTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "finding_triage",
		Description: "Approve a similar finding as distinct or link it as a duplicate without removing its evidence.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"decision":     map[string]any{"type": "string", "enum": []string{"approve", "duplicate"}},
			"duplicate_of": map[string]any{"type": "string", "description": "Required when decision is duplicate."},
		}, "required": []string{"id", "decision"}},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("finding catalog unavailable")
			}
			var params struct {
				ID          string `json:"id"`
				Decision    string `json:"decision"`
				DuplicateOf string `json:"duplicate_of"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			return catalog.TriageFinding(params.ID, params.Decision, params.DuplicateOf)
		},
	}
}

func BuildAssetUpsertTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "asset_upsert",
		Description: "Record or update a discovered asset. Omit value to list current assets.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":    map[string]any{"type": "string"},
				"kind":  map[string]any{"type": "string", "description": "host, url, domain, service, or other."},
				"value": map[string]any{"type": "string"},
				"notes": map[string]any{"type": "string"},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if assetArgsEmpty(args) {
				items, err := catalog.ListAssets()
				if err != nil {
					return nil, err
				}
				return map[string]any{"assets": items, "count": len(items)}, nil
			}
			var a cyber.Asset
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			saved, err := catalog.UpsertAsset(a)
			if err != nil {
				return nil, err
			}
			return saved, nil
		},
	}
}

func BuildScanTool(workDir string, catalog *cyber.Catalog, sb internalsandbox.Sandbox) *agentcore.Tool {
	return BuildScanToolWithTransport(workDir, catalog, sb, nil)
}

func BuildScanToolWithTransport(workDir string, catalog *cyber.Catalog, sb internalsandbox.Sandbox, transport http.RoundTripper) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "scan",
		Description: strings.Join([]string{
			"Run a scan recipe. Use recipe \"list\" to see the catalog (host, DNS, web, verify, cloud, binary, exploitation, intranet).",
			"On the local backend, a missing curl binary falls back to one native HTTP GET if no extra flags are requested. This is limited fingerprint evidence, not a vulnerability scan. Other recipes still require their binaries.",
			"Option-driven recipes take an options object. Scoped network recipes reject arbitrary extra flags; Metasploit requires a single allowed RHOSTS/RHOST and unsupported target-bearing options fail closed.",
			"Network host/URL recipes require parent-set scan_profile allowed_hosts; offline file checks do not. Every active call requires operator approval unless explicitly preauthorized.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"recipe": map[string]any{
					"type":        "string",
					"enum":        cyber.RecipeNames(),
					"description": "Scan recipe to run. Use \"list\" to enumerate recipes.",
				},
				"target": map[string]any{
					"type":        "string",
					"description": "Host, URL, file, query, module, or interface, depending on the recipe.",
				},
				"options": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Named options for option-driven recipes. hydra: service (required), username, username_file, password, password_file, tasks, wait, output. metasploit: set-commands (RHOSTS, LHOST, ...). msfvenom: format, output, encoder, iterations, payload vars. responder: analyze, wpad, fingerprint. impacket: args. netexec: protocol, username, password, hash, module. fscan: ports, mode, username, password, ... bloodhound: username, password, collection, dc. linpeas: mode. hashcat: hash_type (required), attack_mode, wordlist, mask. john: wordlist, format.",
				},
				"extra": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional extra flags, appended verbatim.",
				},
			},
			"required": []any{"recipe"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Recipe  string            `json:"recipe"`
				Target  string            `json:"target"`
				Options map[string]string `json:"options"`
				Extra   []string          `json:"extra"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if catalog != nil && !cyber.AllowOrchestratorTool(subagentRole(ctx), "scan") {
				return nil, fmt.Errorf("scan is reserved for spawned roles (recon, verifier, exploiter, and later kill-chain roles); the parent orchestrates via sessions_spawn")
			}
			recipe := cyber.Recipe(params.Recipe)
			visibleTarget, resultOptions := safeScanTarget(params.Target, recipe, params.Options)
			if strings.EqualFold(params.Recipe, "list") || recipe == cyber.RecipeList {
				return map[string]any{
					"recipes": cyber.RecipeSummaries(),
					"count":   len(cyber.Recipes()),
				}, nil
			}
			if catalog != nil {
				if err := catalog.CheckScanRequestScope(cyber.ScanRequest{Recipe: recipe, Target: params.Target, Options: params.Options, Extra: params.Extra}); err != nil {
					return nil, err
				}
			}
			plan, err := cyber.PlanScan(cyber.ScanRequest{
				Recipe:  recipe,
				Target:  sandboxScanTarget(params.Target, workDir, sb),
				Options: params.Options,
				Extra:   params.Extra,
			})
			if err != nil {
				return nil, fmt.Errorf("scan plan: %s", redactScanSecrets(err.Error(), resultOptions, params.Extra...))
			}
			if ctx == nil {
				ctx = context.Background()
			}
			timeout := plan.Timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			var stdout, stderr bytes.Buffer
			var runErr error
			ok := false
			if sb != nil && sb.Type() != internalsandbox.TypeLocal {
				result, err := sb.Run(ctx, shellCommand(plan.Command), timeout)
				if err != nil {
					runErr = err
				} else {
					stdout.WriteString(result.Stdout)
					stderr.WriteString(result.Stderr)
					ok = result.ExitCode == 0 || allowedExitCode(result.ExitCode, plan.AllowedExit)
					if !ok {
						runErr = fmt.Errorf("scan exited with status %d", result.ExitCode)
					}
				}
			} else {
				bin, err := cyber.LookPath(plan.Binary)
				if err != nil {
					if recipe == cyber.RecipeCurl && transport != nil && len(params.Extra) == 0 && catalog != nil {
						return nativeHTTPFingerprint(ctx, params.Target, catalog, transport)
					}
					return nil, err
				}
				runCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				cmd := exec.CommandContext(runCtx, bin, plan.Command[1:]...)
				cyber.ConfigureProcessGroup(cmd)
				cmd.Cancel = cyber.KillTree(cmd)
				cmd.WaitDelay = 5 * time.Second
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if err := ossandbox.PrepareChildCommand(runCtx, cmd); err != nil {
					return nil, fmt.Errorf("prepare scan process: %w", err)
				}
				runErr = cmd.Run()
				ok = runErr == nil || exitOK(runErr, plan.AllowedExit)
			}
			exchangeID := ""
			if catalog != nil {
				_ = catalog.RecordScanRun(specName(recipe), visibleTarget, ok)
				noteWhiteboxTarget(catalog, params.Target)
				if ok && strings.EqualFold(specName(recipe), "curl") {
					if ex, recErr := catalog.RecordExchange(cyber.Exchange{
						Method:   "GET",
						URL:      params.Target,
						Response: strings.TrimSpace(stdout.String()),
					}); recErr == nil {
						exchangeID = ex.ID
					}
				}
			}
			out := redactScanSecrets(clipScan(stdout.String(), 12000), resultOptions, params.Extra...)
			errOut := redactScanSecrets(clipScan(stderr.String(), 4000), resultOptions, params.Extra...)
			result := map[string]any{
				"recipe":  params.Recipe,
				"target":  visibleTarget,
				"command": redactScanSecrets(strings.Join(plan.Command, " "), resultOptions, params.Extra...),
				"stdout":  out,
				"stderr":  errOut,
			}
			if exchangeID != "" {
				result["exchange_id"] = exchangeID
			}
			if runErr != nil && !exitOK(runErr, plan.AllowedExit) {
				result["error"] = redactScanSecrets(runErr.Error(), resultOptions, params.Extra...)
			}
			return result, nil
		},
	}
}

func safeScanTarget(target string, recipe cyber.Recipe, options map[string]string) (string, map[string]string) {
	secrets := make(map[string]string, len(options)+2)
	for key, value := range options {
		secrets[key] = value
	}
	if recipe == "jwt-analyzer" || recipe == "hashpump" {
		secrets["target_secret"] = target
		return "[redacted]", secrets
	}
	if parsed, err := url.Parse(target); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		for _, values := range parsed.Query() {
			for _, value := range values {
				if value != "" {
					secrets["query_token_"+value] = value
				}
			}
		}
		parsed.RawQuery, parsed.ForceQuery, parsed.Fragment = "", false, ""
		return parsed.String(), secrets
	}
	return target, secrets
}

func redactScanSecrets(text string, options map[string]string, extra ...string) string {
	values := make([]string, 0, len(options)+len(extra))
	isSecret := func(name string) bool {
		name = strings.ToLower(name)
		return strings.Contains(name, "pass") || strings.Contains(name, "pwd") || strings.Contains(name, "hash") ||
			strings.Contains(name, "cookie") || strings.Contains(name, "secret") || strings.Contains(name, "token") ||
			strings.Contains(name, "key") || strings.Contains(name, "auth")
	}
	for key, value := range options {
		if value != "" && isSecret(key) {
			values = append(values, value)
		}
	}
	for i, arg := range extra {
		name, inline, hasInline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if isSecret(name) || name == "u" || name == "user" {
			if hasInline && inline != "" {
				values = append(values, inline)
			} else if i+1 < len(extra) {
				values = append(values, extra[i+1])
			}
		}
		if (name == "H" || name == "header") && i+1 < len(extra) {
			header, value, found := strings.Cut(extra[i+1], ":")
			if found && isSecret(header) && strings.TrimSpace(value) != "" {
				values = append(values, strings.TrimSpace(value))
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	return text
}

func nativeHTTPFingerprint(ctx context.Context, target string, catalog *cyber.Catalog, transport http.RoundTripper) (any, error) {
	visibleTarget, secrets := safeScanTarget(target, cyber.RecipeCurl, nil)
	client := &http.Client{Timeout: 20 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		_ = catalog.RecordScanRun("curl", visibleTarget, false)
		return nil, fmt.Errorf("native HTTP GET: %s", redactScanSecrets(err.Error(), secrets))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	truncated := len(body) > 2<<20
	if truncated {
		body = body[:2<<20]
	}
	result := map[string]any{
		"recipe": "curl", "backend": "native-http", "target": visibleTarget, "command": "native HTTP GET",
		"status": resp.StatusCode, "content_type": resp.Header.Get("Content-Type"),
		"location": redactScanSecrets(resp.Header.Get("Location"), secrets), "server": resp.Header.Get("Server"),
		"stdout": redactScanSecrets(clipScan(string(body), 12000), secrets), "stderr": "", "body_truncated": truncated,
		"evidence_clipped": len(body) > 8000,
	}
	_ = catalog.RecordScanRun("curl", visibleTarget, true)
	if ex, recErr := catalog.RecordExchange(cyber.Exchange{
		Method: http.MethodGet, URL: target, StatusCode: resp.StatusCode,
		Response: string(body),
	}); recErr == nil {
		result["exchange_id"] = ex.ID
	} else {
		result["evidence_error"] = recErr.Error()
	}
	return result, nil
}

func specName(recipe cyber.Recipe) string {
	if spec, ok := cyber.LookupRecipe(recipe); ok {
		return string(spec.Name)
	}
	return string(recipe)
}

func exitOK(err error, allowed []int) bool {
	if err == nil || len(allowed) == 0 {
		return err == nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		code := ee.ExitCode()
		for _, want := range allowed {
			if code == want {
				return true
			}
		}
	}
	return false
}

func findingArgsEmpty(args json.RawMessage) bool {
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "{}" {
		return true
	}
	var params struct {
		Title  string `json:"title"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return false
	}
	return strings.TrimSpace(params.Title) == "" && strings.TrimSpace(params.Target) == ""
}

func assetArgsEmpty(args json.RawMessage) bool {
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "{}" {
		return true
	}
	var params struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return false
	}
	return strings.TrimSpace(params.Value) == ""
}

func clipScan(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]"
}

func allowedExitCode(code int, allowed []int) bool {
	for _, candidate := range allowed {
		if code == candidate {
			return true
		}
	}
	return false
}

func shellCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
	}
	return strings.Join(quoted, " ")
}

func sandboxScanTarget(target, workDir string, sb internalsandbox.Sandbox) string {
	if sb == nil || sb.Type() != internalsandbox.TypeDocker || strings.Contains(target, "://") {
		return target
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	absoluteWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return target
	}
	relative, err := filepath.Rel(absoluteWorkDir, absoluteTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return target
	}
	if relative == "." {
		return "/workspace"
	}
	return filepath.ToSlash(filepath.Join("/workspace", relative))
}
