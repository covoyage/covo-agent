package recovery

import (
	"regexp"
	"strings"
)

type FailoverReason string

const (
	FailoverAuth                             FailoverReason = "auth"
	FailoverAuthPermanent                    FailoverReason = "auth_permanent"
	FailoverBilling                          FailoverReason = "billing"
	FailoverRateLimit                        FailoverReason = "rate_limit"
	FailoverOverloaded                       FailoverReason = "overloaded"
	FailoverServerError                      FailoverReason = "server_error"
	FailoverTimeout                          FailoverReason = "timeout"
	FailoverContextOverflow                  FailoverReason = "context_overflow"
	FailoverPayloadTooLarge                  FailoverReason = "payload_too_large"
	FailoverImageTooLarge                    FailoverReason = "image_too_large"
	FailoverModelNotFound                    FailoverReason = "model_not_found"
	FailoverProviderPolicyBlocked            FailoverReason = "provider_policy_blocked"
	FailoverContentPolicyBlocked             FailoverReason = "content_policy_blocked"
	FailoverFormatError                      FailoverReason = "format_error"
	FailoverThinkingSignature                FailoverReason = "thinking_signature"
	FailoverLongContextTier                  FailoverReason = "long_context_tier"
	FailoverMultimodalToolContentUnsupported FailoverReason = "multimodal_tool_content_unsupported"
	FailoverUnknown                          FailoverReason = "unknown"
)

type ClassifiedError struct {
	Reason           FailoverReason
	StatusCode       int
	Provider         string
	Model            string
	Message          string
	Retryable        bool
	ShouldCompress   bool
	ShouldRotateCred bool
	ShouldFallback   bool
}

func (ce ClassifiedError) IsAuth() bool {
	return ce.Reason == FailoverAuth || ce.Reason == FailoverAuthPermanent
}

func (ce ClassifiedError) IsBilling() bool {
	return ce.Reason == FailoverBilling
}

func (ce ClassifiedError) IsRateLimit() bool {
	return ce.Reason == FailoverRateLimit
}

func (ce ClassifiedError) IsContextOverflow() bool {
	return ce.Reason == FailoverContextOverflow
}

var billingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)insufficient credits`),
	regexp.MustCompile(`(?i)insufficient_quota`),
	regexp.MustCompile(`(?i)insufficient balance`),
	regexp.MustCompile(`(?i)credit balance`),
	regexp.MustCompile(`(?i)credits exhausted`),
	regexp.MustCompile(`(?i)credits have been exhausted`),
	regexp.MustCompile(`(?i)no usable credits`),
	regexp.MustCompile(`(?i)top up your credits`),
	regexp.MustCompile(`(?i)payment required`),
	regexp.MustCompile(`(?i)billing hard limit`),
	regexp.MustCompile(`(?i)exceeded your current quota`),
	regexp.MustCompile(`(?i)account is deactivated`),
	regexp.MustCompile(`(?i)plan does not include`),
	regexp.MustCompile(`(?i)out of funds`),
	regexp.MustCompile(`(?i)run out of funds`),
	regexp.MustCompile(`(?i)balance_depleted`),
	regexp.MustCompile(`(?i)model_not_supported_on_free_tier`),
	regexp.MustCompile(`(?i)not available on the free tier`),
}

var rateLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)rate_limit`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)throttled`),
	regexp.MustCompile(`(?i)requests per minute`),
	regexp.MustCompile(`(?i)tokens per minute`),
	regexp.MustCompile(`(?i)requests per day`),
	regexp.MustCompile(`(?i)try again in`),
	regexp.MustCompile(`(?i)please retry after`),
	regexp.MustCompile(`(?i)resource_exhausted`),
	regexp.MustCompile(`(?i)rate increased too quickly`),
	regexp.MustCompile(`(?i)throttlingexception`),
	regexp.MustCompile(`(?i)too many concurrent requests`),
	regexp.MustCompile(`(?i)servicequotaexceededexception`),
}

var contextOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)context.?(length|window|limit)`),
	regexp.MustCompile(`(?i)token.?(limit|exceeded|maximum)`),
	regexp.MustCompile(`(?i)maximum.?context`),
	regexp.MustCompile(`(?i)too.?long`),
	regexp.MustCompile(`(?i)content.?too.?large`),
	regexp.MustCompile(`(?i)max.?tokens`),
	regexp.MustCompile(`(?i)exceeds.*model.*limit`),
	regexp.MustCompile(`(?i)prompt.*too.*long`),
	regexp.MustCompile(`(?i)input.*too.*long`),
}

var imageTooLargePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)image exceeds`),
	regexp.MustCompile(`(?i)image too large`),
	regexp.MustCompile(`(?i)image_too_large`),
	regexp.MustCompile(`(?i)image size exceeds`),
}

var multimodalToolContentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)text is not set`),
	regexp.MustCompile(`(?i)tool message content must be a string`),
	regexp.MustCompile(`(?i)tool content must be a string`),
	regexp.MustCompile(`(?i)tool message must be a string`),
	regexp.MustCompile(`(?i)expected string, got list`),
	regexp.MustCompile(`(?i)expected string, got array`),
}

var overloadedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)overloaded`),
	regexp.MustCompile(`(?i)service.?overloaded`),
	regexp.MustCompile(`(?i)server.?busy`),
	regexp.MustCompile(`(?i)high.?load`),
	regexp.MustCompile(`(?i)currently.?unavailable`),
}

var modelNotFoundPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)model.*not.?found`),
	regexp.MustCompile(`(?i)model.*does.?not.?exist`),
	regexp.MustCompile(`(?i)invalid.*model`),
	regexp.MustCompile(`(?i)unknown.*model`),
}

var contentPolicyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)content.?filter`),
	regexp.MustCompile(`(?i)safety.?filter`),
	regexp.MustCompile(`(?i)content.?policy`),
	regexp.MustCompile(`(?i)harmful.*content`),
	regexp.MustCompile(`(?i)responsible.*ai`),
	regexp.MustCompile(`(?i)violates.*policy`),
}

var thinkingSignaturePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)thinking.*signature`),
	regexp.MustCompile(`(?i)signing.*key`),
	regexp.MustCompile(`(?i)invalid.*signature`),
}

var longContextTierPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)extra.?usage`),
	regexp.MustCompile(`(?i)long.*context.*tier`),
	regexp.MustCompile(`(?i)context.*tier.*required`),
}

func ClassifyError(err error, statusCode int, provider, model string) ClassifiedError {
	if err == nil {
		return ClassifiedError{
			Reason:    FailoverUnknown,
			Message:   "nil error",
			Retryable: false,
		}
	}

	msg := err.Error()
	ce := ClassifiedError{
		StatusCode: statusCode,
		Provider:   provider,
		Model:      model,
		Message:    msg,
		Reason:     FailoverUnknown,
		Retryable:  true,
	}

	switch {
	case statusCode == 401 || statusCode == 403:
		ce.Reason = FailoverAuth
		ce.ShouldRotateCred = true
		return ce

	case statusCode == 402:
		ce.Reason = FailoverBilling
		ce.ShouldRotateCred = true
		ce.ShouldFallback = true
		return ce

	case statusCode == 429:
		ce.Reason = FailoverRateLimit
		ce.ShouldRotateCred = true
		return ce

	case statusCode == 413:
		ce.Reason = FailoverPayloadTooLarge
		ce.ShouldCompress = true
		return ce

	case statusCode == 404:
		ce.Reason = FailoverModelNotFound
		ce.ShouldFallback = true
		return ce

	case statusCode == 503 || statusCode == 529:
		ce.Reason = FailoverOverloaded
		return ce

	case statusCode >= 500:
		ce.Reason = FailoverServerError
		return ce
	}

	if matchAny(msg, billingPatterns) {
		ce.Reason = FailoverBilling
		ce.ShouldRotateCred = true
		ce.ShouldFallback = true
		return ce
	}

	if matchAny(msg, rateLimitPatterns) {
		ce.Reason = FailoverRateLimit
		ce.ShouldRotateCred = true
		return ce
	}

	if matchAny(msg, contextOverflowPatterns) {
		ce.Reason = FailoverContextOverflow
		ce.ShouldCompress = true
		ce.Retryable = false
		return ce
	}

	if matchAny(msg, imageTooLargePatterns) {
		ce.Reason = FailoverImageTooLarge
		ce.ShouldCompress = true
		return ce
	}

	if matchAny(msg, overloadedPatterns) {
		ce.Reason = FailoverOverloaded
		return ce
	}

	if matchAny(msg, modelNotFoundPatterns) {
		ce.Reason = FailoverModelNotFound
		ce.ShouldFallback = true
		return ce
	}

	if matchAny(msg, contentPolicyPatterns) {
		ce.Reason = FailoverContentPolicyBlocked
		ce.Retryable = false
		return ce
	}

	if matchAny(msg, thinkingSignaturePatterns) {
		ce.Reason = FailoverThinkingSignature
		return ce
	}

	if matchAny(msg, longContextTierPatterns) {
		ce.Reason = FailoverLongContextTier
		ce.ShouldCompress = true
		return ce
	}

	if matchAny(msg, multimodalToolContentPatterns) {
		ce.Reason = FailoverMultimodalToolContentUnsupported
		return ce
	}

	if strings.Contains(strings.ToLower(msg), "timeout") ||
		strings.Contains(strings.ToLower(msg), "timed out") ||
		strings.Contains(strings.ToLower(msg), "deadline exceeded") {
		ce.Reason = FailoverTimeout
		return ce
	}

	return ce
}

func matchAny(msg string, patterns []*regexp.Regexp) bool {
	for _, p := range patterns {
		if p.MatchString(msg) {
			return true
		}
	}
	return false
}

func (r FailoverReason) IsTerminal() bool {
	switch r {
	case FailoverAuthPermanent,
		FailoverContentPolicyBlocked,
		FailoverFormatError,
		FailoverModelNotFound:
		return true
	default:
		return false
	}
}

func (r FailoverReason) NeedsCredentialRotation() bool {
	switch r {
	case FailoverAuth, FailoverBilling, FailoverRateLimit:
		return true
	default:
		return false
	}
}

func (r FailoverReason) NeedsCompression() bool {
	switch r {
	case FailoverContextOverflow, FailoverPayloadTooLarge, FailoverImageTooLarge, FailoverLongContextTier:
		return true
	default:
		return false
	}
}

func (r FailoverReason) NeedsFallback() bool {
	switch r {
	case FailoverBilling, FailoverModelNotFound, FailoverProviderPolicyBlocked:
		return true
	default:
		return false
	}
}
