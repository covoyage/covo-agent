package agent

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RateLimitBucket struct {
	Limit      int
	Remaining  int
	ResetSecs  float64
	CapturedAt time.Time
}

func (b RateLimitBucket) Used() int {
	return max(0, b.Limit-b.Remaining)
}

func (b RateLimitBucket) UsagePct() float64 {
	if b.Limit <= 0 {
		return 0
	}
	return float64(b.Used()) / float64(b.Limit) * 100
}

func (b RateLimitBucket) RemainingSecsNow() float64 {
	elapsed := time.Since(b.CapturedAt).Seconds()
	return max(0, b.ResetSecs-elapsed)
}

func (b RateLimitBucket) IsExhausted() bool {
	return b.Remaining <= 0 && b.Limit > 0
}

type RateLimitState struct {
	mu          sync.RWMutex
	RequestsMin RateLimitBucket
	RequestsHr  RateLimitBucket
	TokensMin   RateLimitBucket
	TokensHr    RateLimitBucket
}

func NewRateLimitState() *RateLimitState {
	return &RateLimitState{}
}

func (s *RateLimitState) Capture(headers http.Header) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.RequestsMin = captureBucket(headers, "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests", now)
	s.RequestsHr = captureBucket(headers, "x-ratelimit-limit-requests-1h", "x-ratelimit-remaining-requests-1h", "x-ratelimit-reset-requests-1h", now)
	s.TokensMin = captureBucket(headers, "x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens", now)
	s.TokensHr = captureBucket(headers, "x-ratelimit-limit-tokens-1h", "x-ratelimit-remaining-tokens-1h", "x-ratelimit-reset-tokens-1h", now)
}

func captureBucket(headers http.Header, limitKey, remainingKey, resetKey string, now time.Time) RateLimitBucket {
	limit := parseHeaderInt(headers, limitKey)
	remaining := parseHeaderInt(headers, remainingKey)
	reset := parseHeaderFloat(headers, resetKey)

	return RateLimitBucket{
		Limit:      limit,
		Remaining:  remaining,
		ResetSecs:  reset,
		CapturedAt: now,
	}
}

func parseHeaderInt(headers http.Header, key string) int {
	vals, ok := headers[http.CanonicalHeaderKey(key)]
	if !ok || len(vals) == 0 {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(vals[0]))
	if err != nil {
		return 0
	}
	return v
}

func parseHeaderFloat(headers http.Header, key string) float64 {
	vals, ok := headers[http.CanonicalHeaderKey(key)]
	if !ok || len(vals) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(vals[0]), 64)
	if err != nil {
		return 0
	}
	return v
}

func (s *RateLimitState) Snapshot() RateLimitSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return RateLimitSnapshot{
		RequestsMin: s.RequestsMin,
		RequestsHr:  s.RequestsHr,
		TokensMin:   s.TokensMin,
		TokensHr:    s.TokensHr,
	}
}

func (s *RateLimitState) AnyExhausted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.RequestsMin.IsExhausted() || s.RequestsHr.IsExhausted() ||
		s.TokensMin.IsExhausted() || s.TokensHr.IsExhausted()
}

func (s *RateLimitState) ShouldBackoff() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if bucketShouldBackoff(s.RequestsMin) {
		return true
	}
	if bucketShouldBackoff(s.TokensMin) {
		return true
	}
	return false
}

func bucketShouldBackoff(bucket RateLimitBucket) bool {
	return bucket.IsExhausted() && bucket.RemainingSecsNow() > 0
}

type RateLimitSnapshot struct {
	RequestsMin RateLimitBucket `json:"requests_min"`
	RequestsHr  RateLimitBucket `json:"requests_hr"`
	TokensMin   RateLimitBucket `json:"tokens_min"`
	TokensHr    RateLimitBucket `json:"tokens_hr"`
}

func (s RateLimitSnapshot) String() string {
	var parts []string

	addBucket := func(label string, b RateLimitBucket) {
		if b.Limit > 0 {
			remSecs := b.RemainingSecsNow()
			if remSecs > 0 {
				parts = append(parts, fmt.Sprintf("%s: %d/%d remaining (%.0fs to reset)",
					label, b.Remaining, b.Limit, remSecs))
			} else {
				parts = append(parts, fmt.Sprintf("%s: %d/%d remaining (resetting)",
					label, b.Remaining, b.Limit))
			}
		}
	}

	addBucket("RPM", s.RequestsMin)
	addBucket("RPH", s.RequestsHr)
	addBucket("TPM", s.TokensMin)
	addBucket("TPH", s.TokensHr)

	if len(parts) == 0 {
		return "no rate limit data captured"
	}

	return strings.Join(parts, "\n")
}
