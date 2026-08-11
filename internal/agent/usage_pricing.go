package agent

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type CostSource string

const (
	CostSourceOfficialDocs CostSource = "official_docs_snapshot"
	CostSourceUserOverride CostSource = "user_override"
	CostSourceNone         CostSource = "none"
)

type CostStatus string

const (
	CostStatusActual    CostStatus = "actual"
	CostStatusEstimated CostStatus = "estimated"
	CostStatusIncluded  CostStatus = "included"
	CostStatusUnknown   CostStatus = "unknown"
)

type CanonicalUsage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	RequestCount     int
}

func (u *CanonicalUsage) PromptTokens() int {
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

func (u *CanonicalUsage) TotalTokens() int {
	return u.PromptTokens() + u.OutputTokens
}

type PricingEntry struct {
	InputCostPerMillion      float64
	OutputCostPerMillion     float64
	CacheReadCostPerMillion  float64
	CacheWriteCostPerMillion float64
	RequestCost              float64
	Source                   CostSource
	SourceURL                string
	PricingVersion           string
}

type CostResult struct {
	AmountUSD      float64
	Status         CostStatus
	Source         CostSource
	Label          string
	PricingVersion string
	Notes          []string
}

var pricingTable = map[string]PricingEntry{
	"anthropic:claude-opus-4-8": {
		InputCostPerMillion:      5.00,
		OutputCostPerMillion:     25.00,
		CacheReadCostPerMillion:  0.50,
		CacheWriteCostPerMillion: 6.25,
		Source:                   CostSourceOfficialDocs,
		SourceURL:                "https://platform.claude.com/docs/en/about-claude/pricing",
		PricingVersion:           "anthropic-pricing-2026-05",
	},
	"anthropic:claude-sonnet-4-6": {
		InputCostPerMillion:      3.00,
		OutputCostPerMillion:     15.00,
		CacheReadCostPerMillion:  0.30,
		CacheWriteCostPerMillion: 3.75,
		Source:                   CostSourceOfficialDocs,
		SourceURL:                "https://platform.claude.com/docs/en/about-claude/pricing",
		PricingVersion:           "anthropic-pricing-2026-05",
	},
	"anthropic:claude-haiku-4-5": {
		InputCostPerMillion:      1.00,
		OutputCostPerMillion:     5.00,
		CacheReadCostPerMillion:  0.10,
		CacheWriteCostPerMillion: 1.25,
		Source:                   CostSourceOfficialDocs,
		SourceURL:                "https://platform.claude.com/docs/en/about-claude/pricing",
		PricingVersion:           "anthropic-pricing-2026-05",
	},
	"openai:gpt-4o": {
		InputCostPerMillion:     2.50,
		OutputCostPerMillion:    10.00,
		CacheReadCostPerMillion: 1.25,
		Source:                  CostSourceOfficialDocs,
		SourceURL:               "https://openai.com/api/pricing/",
		PricingVersion:          "openai-pricing-2026-03",
	},
	"openai:gpt-4.1": {
		InputCostPerMillion:     2.00,
		OutputCostPerMillion:    8.00,
		CacheReadCostPerMillion: 0.50,
		Source:                  CostSourceOfficialDocs,
		SourceURL:               "https://openai.com/api/pricing/",
		PricingVersion:          "openai-pricing-2026-03",
	},
	"openai:gpt-4.1-mini": {
		InputCostPerMillion:     0.40,
		OutputCostPerMillion:    1.60,
		CacheReadCostPerMillion: 0.10,
		Source:                  CostSourceOfficialDocs,
		SourceURL:               "https://openai.com/api/pricing/",
		PricingVersion:          "openai-pricing-2026-03",
	},
	"openai:o3": {
		InputCostPerMillion:     10.00,
		OutputCostPerMillion:    40.00,
		CacheReadCostPerMillion: 2.50,
		Source:                  CostSourceOfficialDocs,
		SourceURL:               "https://openai.com/api/pricing/",
		PricingVersion:          "openai-pricing-2026-03",
	},
	"openai:o3-mini": {
		InputCostPerMillion:     1.10,
		OutputCostPerMillion:    4.40,
		CacheReadCostPerMillion: 0.55,
		Source:                  CostSourceOfficialDocs,
		SourceURL:               "https://openai.com/api/pricing/",
		PricingVersion:          "openai-pricing-2026-03",
	},
}

var userPricingOverrides = make(map[string]PricingEntry)
var pricingMu sync.RWMutex

func SetUserPricingOverride(provider, model string, entry PricingEntry) {
	key := fmt.Sprintf("%s:%s", provider, model)
	pricingMu.Lock()
	defer pricingMu.Unlock()
	entry.Source = CostSourceUserOverride
	userPricingOverrides[key] = entry
}

func LookupPricing(provider, model string) (PricingEntry, bool) {
	key := fmt.Sprintf("%s:%s", provider, model)

	pricingMu.RLock()
	if override, ok := userPricingOverrides[key]; ok {
		pricingMu.RUnlock()
		return override, true
	}
	pricingMu.RUnlock()

	if entry, ok := pricingTable[key]; ok {
		return entry, true
	}

	return PricingEntry{Source: CostSourceNone}, false
}

func CalculateCost(usage *CanonicalUsage, pricing *PricingEntry) *CostResult {
	if usage == nil || pricing == nil || pricing.Source == CostSourceNone {
		return &CostResult{
			AmountUSD: 0,
			Status:    CostStatusUnknown,
			Source:    CostSourceNone,
			Label:     "unknown",
		}
	}

	perMillion := 1000000.0

	inputCost := float64(usage.InputTokens) / perMillion * pricing.InputCostPerMillion
	outputCost := float64(usage.OutputTokens) / perMillion * pricing.OutputCostPerMillion
	cacheReadCost := float64(usage.CacheReadTokens) / perMillion * pricing.CacheReadCostPerMillion
	cacheWriteCost := float64(usage.CacheWriteTokens) / perMillion * pricing.CacheWriteCostPerMillion
	requestCost := pricing.RequestCost * float64(usage.RequestCount)

	total := inputCost + outputCost + cacheReadCost + cacheWriteCost + requestCost

	status := CostStatusEstimated
	if pricing.Source == CostSourceOfficialDocs {
		status = CostStatusActual
	}

	label := fmt.Sprintf("%s/%s", pricing.Source, pricing.PricingVersion)

	return &CostResult{
		AmountUSD:      math.Round(total*1000000) / 1000000,
		Status:         status,
		Source:         pricing.Source,
		Label:          label,
		PricingVersion: pricing.PricingVersion,
	}
}

type CostTracker struct {
	mu               sync.Mutex
	provider         string
	model            string
	accumulatedUsage *CanonicalUsage
	accumulatedCost  float64
	history          []CostRecord
	lastPromptTokens int64 // from the most recent API response; = current context size
}

type CostRecord struct {
	Timestamp time.Time
	Usage     *CanonicalUsage
	Cost      *CostResult
}

func NewCostTracker(provider, model string) *CostTracker {
	return &CostTracker{
		provider:         provider,
		model:            model,
		accumulatedUsage: &CanonicalUsage{RequestCount: 0},
	}
}

func (t *CostTracker) RecordUsage(usage *CanonicalUsage) *CostResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.accumulatedUsage.InputTokens += usage.InputTokens
	t.accumulatedUsage.OutputTokens += usage.OutputTokens
	t.accumulatedUsage.CacheReadTokens += usage.CacheReadTokens
	t.accumulatedUsage.CacheWriteTokens += usage.CacheWriteTokens
	t.accumulatedUsage.ReasoningTokens += usage.ReasoningTokens
	t.accumulatedUsage.RequestCount += usage.RequestCount

	pricing, _ := LookupPricing(t.provider, t.model)
	result := CalculateCost(usage, &pricing)
	t.accumulatedCost += result.AmountUSD

	t.history = append(t.history, CostRecord{
		Timestamp: time.Now(),
		Usage:     usage,
		Cost:      result,
	})

	return result
}

func (t *CostTracker) CurrentCost() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return math.Round(t.accumulatedCost*1000000) / 1000000
}

func (t *CostTracker) CurrentUsage() *CanonicalUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := *t.accumulatedUsage
	return &u
}

// RecordPromptTokens records the raw prompt_tokens from an API response.
// This represents the current context size (how many tokens are in the
// messages sent to the API), which is distinct from the cumulative total.
func (t *CostTracker) RecordPromptTokens(promptTokens int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastPromptTokens = promptTokens
}

// LastPromptTokens returns the prompt_tokens from the most recent API
// response. This represents the current context window usage, suitable
// for computing a percentage against ContextLength().
func (t *CostTracker) LastPromptTokens() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastPromptTokens
}

func (t *CostTracker) History() []CostRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := make([]CostRecord, len(t.history))
	copy(h, t.history)
	return h
}

func (t *CostTracker) Summary() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return fmt.Sprintf(
		"Usage: %d input / %d output / %d cache-read / %d cache-write tokens | %d requests | $%.6f total",
		t.accumulatedUsage.InputTokens,
		t.accumulatedUsage.OutputTokens,
		t.accumulatedUsage.CacheReadTokens,
		t.accumulatedUsage.CacheWriteTokens,
		t.accumulatedUsage.RequestCount,
		math.Round(t.accumulatedCost*1000000)/1000000,
	)
}
