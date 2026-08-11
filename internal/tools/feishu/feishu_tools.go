package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/covoyage/covonaut/agentcore"
)

// FeishuClient wraps the Feishu Open API for document/wiki/bitable operations.
type FeishuClient struct {
	appID       string
	appSecret   string
	accessToken string
	httpClient  *http.Client
	enabled     bool
}

// NewFeishuClient creates a client from FEISHU_APP_ID / FEISHU_APP_SECRET env vars.
func NewFeishuClient() *FeishuClient {
	// Also check LARK_ prefix as fallback
	appID := os.Getenv("FEISHU_APP_ID")
	if appID == "" {
		appID = os.Getenv("LARK_APP_ID")
	}
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	if appSecret == "" {
		appSecret = os.Getenv("LARK_APP_SECRET")
	}
	return &FeishuClient{
		appID:      appID,
		appSecret:  appSecret,
		httpClient: &http.Client{},
		enabled:    appID != "" && appSecret != "",
	}
}

func (c *FeishuClient) Enabled() bool { return c.enabled }

func (c *FeishuClient) ensureToken(ctx context.Context) error {
	if c.accessToken != "" {
		return nil
	}
	body := map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu auth: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Code != 0 {
		return fmt.Errorf("feishu auth failed (code %d): %s", result.Code, result.Msg)
	}
	c.accessToken = result.TenantAccessToken
	return nil
}

func (c *FeishuClient) doAPI(ctx context.Context, method, path string, body map[string]any) ([]byte, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	url := "https://open.feishu.cn/open-apis" + path
	var reqBody []byte
	if body != nil {
		reqBody, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("feishu request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu api: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return respBody, nil
}

func checkFeishuEnabled(client *FeishuClient) error {
	if !client.Enabled() {
		return fmt.Errorf("feishu: FEISHU_APP_ID or LARK_APP_ID and APP_SECRET not configured")
	}
	return nil
}

// ---- Doc Tools ----

func BuildFeishuDocReadTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_doc_read",
		Description: "Read the raw content of a Feishu/Lark document. Provide the doc_token from the document URL (e.g. https://xxx.feishu.cn/docx/TOKEN).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"doc_token": map[string]any{
					"type":        "string",
					"description": "Feishu document token from URL.",
				},
			},
			"required": []string{"doc_token"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				DocToken string `json:"doc_token"`
			}
			json.Unmarshal(args, &params)
			if params.DocToken == "" {
				return nil, fmt.Errorf("doc_token is required")
			}
			resp, err := client.doAPI(ctx, "GET", "/docx/v1/documents/"+params.DocToken, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"raw": string(resp)}, nil
		},
	}
}

func BuildFeishuDocCreateTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_doc_create",
		Description: "Create a new Feishu/Lark document with an optional folder location.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Document title.",
				},
				"folder_token": map[string]any{
					"type":        "string",
					"description": "Parent folder token (optional).",
				},
			},
			"required": []string{"title"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				Title       string `json:"title"`
				FolderToken string `json:"folder_token"`
			}
			json.Unmarshal(args, &params)
			body := map[string]any{"title": params.Title}
			if params.FolderToken != "" {
				body["folder_token"] = params.FolderToken
			}
			resp, err := client.doAPI(ctx, "POST", "/docx/v1/documents", body)
			if err != nil {
				return nil, err
			}
			return map[string]any{"result": string(resp)}, nil
		},
	}
}

// ---- Wiki / Knowledge Base Tools ----

func BuildFeishuWikiListTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_wiki_list",
		Description: "List all wiki spaces in the Feishu/Lark tenant.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			resp, err := client.doAPI(ctx, "GET", "/wiki/v2/spaces?page_size=50", nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"spaces": string(resp)}, nil
		},
	}
}

func BuildFeishuWikiNodesTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_wiki_nodes",
		Description: "List child nodes (pages) in a wiki space, optionally under a specific parent node.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"space_id": map[string]any{
					"type":        "string",
					"description": "Wiki space ID.",
				},
				"parent_token": map[string]any{
					"type":        "string",
					"description": "Parent node token (optional).",
				},
			},
			"required": []string{"space_id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				SpaceID     string `json:"space_id"`
				ParentToken string `json:"parent_token"`
			}
			json.Unmarshal(args, &params)
			path := fmt.Sprintf("/wiki/v2/spaces/%s/nodes?page_size=50", params.SpaceID)
			if params.ParentToken != "" {
				path += "&parent_node_token=" + params.ParentToken
			}
			resp, err := client.doAPI(ctx, "GET", path, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"nodes": string(resp)}, nil
		},
	}
}

// ---- Bitable (Smart Spreadsheet) Tools ----

func BuildFeishuBitableListTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_bitable_list",
		Description: "List all tables in a Feishu/Lark Bitable (smart spreadsheet). Provide the app_token from the Bitable URL.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"app_token": map[string]any{
					"type":        "string",
					"description": "Bitable app token from URL.",
				},
			},
			"required": []string{"app_token"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				AppToken string `json:"app_token"`
			}
			json.Unmarshal(args, &params)
			resp, err := client.doAPI(ctx, "GET", "/bitable/v1/apps/"+params.AppToken+"/tables", nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"tables": string(resp)}, nil
		},
	}
}

func BuildFeishuBitableRecordsTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_bitable_records",
		Description: "List records (rows) from a Feishu/Lark Bitable table, with optional filtering and sorting.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"app_token": map[string]any{
					"type":        "string",
					"description": "Bitable app token.",
				},
				"table_id": map[string]any{
					"type":        "string",
					"description": "Table ID.",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Records per page (default: 100, max: 500).",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter expression (e.g. CurrentValue.[status] = \"active\").",
				},
			},
			"required": []string{"app_token", "table_id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				AppToken string `json:"app_token"`
				TableID  string `json:"table_id"`
				PageSize int    `json:"page_size"`
				Filter   string `json:"filter"`
			}
			json.Unmarshal(args, &params)
			if params.PageSize <= 0 {
				params.PageSize = 100
			}
			path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records?page_size=%d",
				params.AppToken, params.TableID, params.PageSize)
			if params.Filter != "" {
				path += "&filter=" + params.Filter
			}
			resp, err := client.doAPI(ctx, "GET", path, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"records": string(resp)}, nil
		},
	}
}

// ---- Drive Tools ----

func BuildFeishuDriveListTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_drive_list",
		Description: "List files and folders in a Feishu/Lark drive folder. Use folder_token=\"root\" for the root folder.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"folder_token": map[string]any{
					"type":        "string",
					"description": "Folder token (use \"root\" for root, or provide a specific folder token).",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Items per page (default: 50).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				FolderToken string `json:"folder_token"`
				PageSize    int    `json:"page_size"`
			}
			json.Unmarshal(args, &params)
			if params.FolderToken == "" {
				params.FolderToken = "root"
			}
			if params.PageSize <= 0 {
				params.PageSize = 50
			}
			path := fmt.Sprintf("/drive/v1/files?folder_token=%s&page_size=%d", params.FolderToken, params.PageSize)
			resp, err := client.doAPI(ctx, "GET", path, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"files": string(resp)}, nil
		},
	}
}

func BuildFeishuDriveDownloadTool(client *FeishuClient) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "feishu_drive_download",
		Description: "Download the raw content or metadata of a Feishu/Lark Drive file by file token.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "File token to download.",
				},
			},
			"required": []string{"file_token"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := checkFeishuEnabled(client); err != nil {
				return nil, err
			}
			var params struct {
				FileToken string `json:"file_token"`
			}
			json.Unmarshal(args, &params)
			if params.FileToken == "" {
				return nil, fmt.Errorf("file_token is required")
			}
			// Get file metadata first
			meta, err := client.doAPI(ctx, "GET", "/drive/v1/files/"+params.FileToken, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"file": string(meta)}, nil
		},
	}
}
