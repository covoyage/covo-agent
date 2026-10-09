package cyber

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Exchange is one recorded HTTP request/response. Findings cite Exchange.ID
// instead of pasting a truncated body into the conversation.
type Exchange struct {
	ID                string    `json:"id"`
	Redacted          bool      `json:"redacted,omitempty"`
	HeadersTruncated  bool      `json:"headers_truncated,omitempty"`
	RequestTruncated  bool      `json:"request_truncated,omitempty"`
	ResponseTruncated bool      `json:"response_truncated,omitempty"`
	Method            string    `json:"method"`
	URL               string    `json:"url"`
	StatusCode        int       `json:"status_code,omitempty"`
	ReqHeaders        string    `json:"req_headers,omitempty"`
	Request           string    `json:"request,omitempty"`
	Response          string    `json:"response,omitempty"`
	Identity          string    `json:"identity,omitempty"`
	Role              string    `json:"role,omitempty"`
	Tenant            string    `json:"tenant,omitempty"`
	Resource          string    `json:"resource,omitempty"`
	Owner             string    `json:"owner,omitempty"`
	BaselineID        string    `json:"baseline_id,omitempty"`
	At                time.Time `json:"at"`
}

// ExchangeFilter selects recorded requests by protocol metadata or content.
type ExchangeFilter struct {
	Method     string `json:"method"`
	Host       string `json:"host"`
	Path       string `json:"path"`
	Parameter  string `json:"parameter"`
	Identity   string `json:"identity"`
	Tenant     string `json:"tenant"`
	Resource   string `json:"resource"`
	StatusCode int    `json:"status_code"`
	Text       string `json:"text"`
	Limit      int    `json:"limit"`
}

// ExchangeSummary is the compact searchable view of an HTTP exchange.
type ExchangeSummary struct {
	ID         string    `json:"id"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Host       string    `json:"host,omitempty"`
	Path       string    `json:"path,omitempty"`
	Parameters []string  `json:"parameters,omitempty"`
	Identity   string    `json:"identity,omitempty"`
	Role       string    `json:"role,omitempty"`
	Tenant     string    `json:"tenant,omitempty"`
	Resource   string    `json:"resource,omitempty"`
	Owner      string    `json:"owner,omitempty"`
	BaselineID string    `json:"baseline_id,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	At         time.Time `json:"at"`
}

func (c *Catalog) exchangesPath() string { return filepath.Join(c.dir, "exchanges.json") }

func (c *Catalog) ConsumeReplayBudget() error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	path := filepath.Join(c.dir, "replay_budget.json")
	var budget struct {
		Day   string `json:"day"`
		Count int    `json:"count"`
	}
	if err := c.readJSON(path, &budget); err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	if budget.Day != today {
		budget.Day, budget.Count = today, 0
	}
	if budget.Count >= 200 {
		return fmt.Errorf("daily replay request budget exhausted (200); retry tomorrow")
	}
	budget.Count++
	return c.writeJSON(path, budget)
}

// RecordExchange appends a bounded exchange and returns its id (ex-N).
func (c *Catalog) RecordExchange(ex Exchange) (Exchange, error) {
	if c == nil {
		return Exchange{}, fmt.Errorf("nil catalog")
	}
	ex.Method = strings.ToUpper(strings.TrimSpace(ex.Method))
	if ex.Method == "" {
		ex.Method = "GET"
	}
	ex.URL = strings.TrimSpace(ex.URL)
	if ex.URL == "" {
		return Exchange{}, fmt.Errorf("exchange url is required")
	}
	ex.HeadersTruncated = ex.HeadersTruncated || len(ex.ReqHeaders) > 2000
	ex.RequestTruncated = ex.RequestTruncated || len(ex.Request) > 4000
	ex.ResponseTruncated = ex.ResponseTruncated || len(ex.Response) > 8000
	ex.ReqHeaders = clip(ex.ReqHeaders, 2000)
	if len(ex.Request) > 4000 {
		ex.Request = ex.Request[:4000]
	}
	ex.Response = clip(ex.Response, 8000)
	ex.Identity = clip(ex.Identity, 128)
	ex.Role = clip(ex.Role, 128)
	ex.Tenant = clip(ex.Tenant, 128)
	ex.Resource = clip(ex.Resource, 256)
	ex.Owner = clip(ex.Owner, 128)
	ex.BaselineID = strings.TrimSpace(ex.BaselineID)
	ex.At = time.Now().UTC()

	catalogMu.Lock()
	items, err := c.loadExchangesLocked()
	if err != nil {
		catalogMu.Unlock()
		return Exchange{}, err
	}
	if ex.BaselineID != "" {
		found := false
		for _, item := range items {
			if item.ID == ex.BaselineID {
				found = true
				break
			}
		}
		if !found {
			catalogMu.Unlock()
			return Exchange{}, fmt.Errorf("baseline exchange %q not found", ex.BaselineID)
		}
	}
	next := len(items) + 1
	for _, item := range items {
		var n int
		if _, err := fmt.Sscanf(item.ID, "ex-%d", &n); err == nil && n >= next {
			next = n + 1
		}
	}
	ex.ID = fmt.Sprintf("ex-%d", next)
	items = append(items, ex)
	if len(items) > 500 {
		items, err = c.retainExchangesLocked(items, ex.ID)
		if err != nil {
			catalogMu.Unlock()
			return Exchange{}, err
		}
	}
	if err := c.writeJSON(c.exchangesPath(), items); err != nil {
		catalogMu.Unlock()
		return Exchange{}, err
	}
	if ex.BaselineID == "" {
		if _, _, err := c.queueExchangeLocked(ex); err != nil {
			catalogMu.Unlock()
			return ex, fmt.Errorf("exchange %s saved but intake queue failed: %w", ex.ID, err)
		}
	}
	catalogMu.Unlock()
	return ex, nil
}

func (c *Catalog) retainExchangesLocked(items []Exchange, newestID string) ([]Exchange, error) {
	findings, err := c.loadFindingsLocked()
	if err != nil {
		return nil, err
	}
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Exchange, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	chain := func(id string) []string {
		var ids []string
		seen := map[string]bool{}
		for id != "" && !seen[id] {
			item, found := byID[id]
			if !found {
				break
			}
			seen[id] = true
			ids = append(ids, id)
			id = item.BaselineID
		}
		return ids
	}
	retained := map[string]bool{}
	protect := func(id string) {
		for _, referenced := range chain(id) {
			retained[referenced] = true
		}
	}
	protect(newestID)
	for _, finding := range findings {
		protect(finding.ExchangeID)
	}
	for _, job := range jobs {
		if job.Status == "pending" || job.Status == "running" {
			protect(job.ExchangeID)
		}
	}
	if len(retained) > 500 {
		return nil, fmt.Errorf("exchange storage is full (500 records); referenced evidence cannot be evicted")
	}
	for index := len(items) - 1; index >= 0 && len(retained) < 500; index-- {
		ids := chain(items[index].ID)
		additional := 0
		for _, id := range ids {
			if !retained[id] {
				additional++
			}
		}
		if len(retained)+additional <= 500 {
			protect(items[index].ID)
		}
	}
	result := make([]Exchange, 0, len(retained))
	for _, item := range items {
		if retained[item.ID] {
			result = append(result, item)
		}
	}
	return result, nil
}

func (c *Catalog) ListExchanges() ([]Exchange, error) {
	if c == nil {
		return nil, nil
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return c.loadExchangesLocked()
}

func (c *Catalog) GetExchange(id string) (Exchange, bool, error) {
	items, err := c.ListExchanges()
	if err != nil {
		return Exchange{}, false, err
	}
	id = strings.TrimSpace(id)
	for _, item := range items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return Exchange{}, false, nil
}

// LabelExchange adds non-secret identity context without modifying captured request data.
func (c *Catalog) LabelExchange(id string, labels Exchange) (Exchange, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	items, err := c.loadExchangesLocked()
	if err != nil {
		return Exchange{}, err
	}
	for i := range items {
		if items[i].ID != strings.TrimSpace(id) {
			continue
		}
		if labels.BaselineID != "" {
			found := false
			for _, ex := range items {
				if ex.ID == labels.BaselineID && ex.ID != id {
					found = true
				}
			}
			if !found {
				return Exchange{}, fmt.Errorf("baseline exchange %q not found", labels.BaselineID)
			}
		}
		if labels.Identity != "" {
			items[i].Identity = clip(labels.Identity, 128)
		}
		if labels.Role != "" {
			items[i].Role = clip(labels.Role, 128)
		}
		if labels.Tenant != "" {
			items[i].Tenant = clip(labels.Tenant, 128)
		}
		if labels.Resource != "" {
			items[i].Resource = clip(labels.Resource, 256)
		}
		if labels.Owner != "" {
			items[i].Owner = clip(labels.Owner, 128)
		}
		if labels.BaselineID != "" {
			items[i].BaselineID = strings.TrimSpace(labels.BaselineID)
		}
		if err := c.writeJSON(c.exchangesPath(), items); err != nil {
			return Exchange{}, err
		}
		if err := c.syncIntakeLabelLocked(items[i]); err != nil {
			return items[i], fmt.Errorf("exchange labeled but intake queue update failed: %w", err)
		}
		return items[i], nil
	}
	return Exchange{}, fmt.Errorf("exchange %q not found", id)
}

// SearchExchanges returns newest-first summaries matching every populated filter.
func (c *Catalog) SearchExchanges(filter ExchangeFilter) ([]ExchangeSummary, error) {
	items, err := c.ListExchanges()
	if err != nil {
		return nil, err
	}
	filter.Method = strings.ToUpper(strings.TrimSpace(filter.Method))
	filter.Host = strings.ToLower(strings.TrimSpace(filter.Host))
	filter.Path = strings.TrimSpace(filter.Path)
	filter.Parameter = strings.ToLower(strings.TrimSpace(filter.Parameter))
	filter.Identity = strings.ToLower(strings.TrimSpace(filter.Identity))
	filter.Tenant = strings.ToLower(strings.TrimSpace(filter.Tenant))
	filter.Resource = strings.ToLower(strings.TrimSpace(filter.Resource))
	filter.Text = strings.ToLower(strings.TrimSpace(filter.Text))
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	result := make([]ExchangeSummary, 0, min(filter.Limit, len(items)))
	for i := len(items) - 1; i >= 0 && len(result) < filter.Limit; i-- {
		ex := items[i]
		parsed, _ := url.Parse(ex.URL)
		params := exchangeParameterNames(ex, parsed)
		if filter.Method != "" && ex.Method != filter.Method {
			continue
		}
		if filter.Host != "" && !strings.Contains(strings.ToLower(parsed.Hostname()), filter.Host) {
			continue
		}
		if filter.Path != "" && !strings.Contains(parsed.EscapedPath(), filter.Path) {
			continue
		}
		if filter.Parameter != "" && !containsFold(params, filter.Parameter) {
			continue
		}
		if filter.Identity != "" && strings.ToLower(ex.Identity) != filter.Identity {
			continue
		}
		if filter.Tenant != "" && strings.ToLower(ex.Tenant) != filter.Tenant {
			continue
		}
		if filter.Resource != "" && strings.ToLower(ex.Resource) != filter.Resource {
			continue
		}
		if filter.StatusCode != 0 && ex.StatusCode != filter.StatusCode {
			continue
		}
		if filter.Text != "" && !strings.Contains(strings.ToLower(ex.URL+"\n"+ex.Request+"\n"+ex.Response), filter.Text) {
			continue
		}
		result = append(result, ExchangeSummary{
			ID: ex.ID, Method: ex.Method, URL: ex.URL, Host: parsed.Hostname(),
			Path: parsed.EscapedPath(), Parameters: params, Identity: ex.Identity, Role: ex.Role,
			Tenant: ex.Tenant, Resource: ex.Resource, Owner: ex.Owner, BaselineID: ex.BaselineID,
			StatusCode: ex.StatusCode, At: ex.At,
		})
	}
	return result, nil
}

func exchangeParameterNames(ex Exchange, parsed *url.URL) []string {
	seen := map[string]bool{}
	if parsed != nil {
		for name := range parsed.Query() {
			seen[strings.ToLower(name)] = true
		}
	}
	if !json.Valid([]byte(ex.Request)) {
		if values, err := url.ParseQuery(ex.Request); err == nil {
			for name := range values {
				seen[strings.ToLower(name)] = true
			}
		}
	}
	var body map[string]any
	if json.Unmarshal([]byte(ex.Request), &body) == nil {
		for name := range body {
			seen[strings.ToLower(name)] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsFold(items []string, wanted string) bool {
	for _, item := range items {
		if strings.EqualFold(item, wanted) {
			return true
		}
	}
	return false
}

func (c *Catalog) loadExchangesLocked() ([]Exchange, error) {
	var items []Exchange
	if err := c.readJSON(c.exchangesPath(), &items); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return items, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
