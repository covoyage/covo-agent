package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/url"
	"sort"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covonaut/tools"
)

func cyberBrowserNetworkConfig(catalog *cyber.Catalog, logger *slog.Logger) *tools.BrowserNetworkConfig {
	if logger == nil {
		logger = slog.Default()
	}
	allowed := func(raw string) bool {
		if catalog == nil {
			return false
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return false
		}
		config, err := catalog.IntakeConfig()
		if err != nil || !config.Enabled {
			return false
		}
		for _, host := range config.Hosts {
			if strings.EqualFold(host, parsed.Hostname()) {
				return true
			}
		}
		return false
	}
	return &tools.BrowserNetworkConfig{
		AllowURL: allowed,
		OnError:  func(err error) { logger.Warn("browser network observation incomplete", "error", err) },
		OnExchange: func(item tools.BrowserNetworkExchange) {
			if !allowed(item.URL) {
				return
			}
			exchange, err := browserNetworkEvidence(item)
			if err != nil {
				logger.Warn("browser network evidence rejected", "reason", err)
				return
			}
			if _, err := catalog.RecordExchange(exchange); err != nil {
				logger.Warn("browser network evidence could not be saved", "error", err)
			}
		},
	}
}

func browserSecretField(name string) bool {
	name = strings.ToLower(name)
	for _, word := range []string{"auth", "token", "secret", "password", "passwd", "cookie", "session", "credential", "key", "csrf", "xsrf", "nonce", "bearer", "code", "otp", "pin", "email", "phone"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func redactBrowserValues(raw string) (string, bool, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", false, err
	}
	changed := false
	for name, items := range values {
		for index, value := range items {
			filtered := RedactSensitiveText(value, true, false)
			if browserSecretField(name) {
				filtered = "[redacted]"
			}
			if filtered != value {
				changed = true
				values[name][index] = filtered
			}
		}
	}
	if changed {
		return values.Encode(), true, nil
	}
	return raw, false, nil
}

func redactBrowserJSON(raw string) (string, bool, error) {
	if !json.Valid([]byte(raw)) {
		return "", false, fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	count := 0
	if err := validateBrowserJSON(decoder, 0, &count); err != nil {
		return "", false, err
	}
	decoder = json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var body any
	if err := decoder.Decode(&body); err != nil {
		return "", false, err
	}
	changed := false
	nodes := 0
	var walk func(any, int) error
	walk = func(value any, depth int) error {
		nodes++
		if nodes > 128 || depth > 8 {
			return fmt.Errorf("JSON exceeds filtering budget")
		}
		switch item := value.(type) {
		case map[string]any:
			for name, child := range item {
				if browserSecretField(name) {
					item[name], changed = "[redacted]", true
					continue
				}
				if text, ok := child.(string); ok {
					filtered := RedactSensitiveText(text, true, false)
					if filtered != text {
						item[name], changed = filtered, true
					}
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for index, child := range item {
				if text, ok := child.(string); ok {
					filtered := RedactSensitiveText(text, true, false)
					if filtered != text {
						item[index], changed = filtered, true
					}
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(body, 0); err != nil {
		return "", false, err
	}
	if !changed {
		filtered := RedactSensitiveText(raw, true, false)
		return filtered, filtered != raw, nil
	}
	encoded, err := json.Marshal(body)
	return string(encoded), true, err
}

func validateBrowserJSON(decoder *json.Decoder, depth int, count *int) error {
	*count += 1
	if depth > 8 || *count > 128 {
		return fmt.Errorf("JSON exceeds filtering budget")
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
				return fmt.Errorf("JSON contains duplicate keys")
			}
			seen[name] = true
		}
		if err := validateBrowserJSON(decoder, depth+1, count); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func browserNetworkEvidence(item tools.BrowserNetworkExchange) (cyber.Exchange, error) {
	parsed, err := url.Parse(item.URL)
	if err != nil || parsed.User != nil || len(item.URL) > 8000 {
		return cyber.Exchange{}, fmt.Errorf("invalid or oversized browser URL")
	}
	query, redacted, err := redactBrowserValues(parsed.RawQuery)
	if err != nil {
		return cyber.Exchange{}, fmt.Errorf("browser query cannot be filtered")
	}
	parsed.RawQuery = query
	parsed.Fragment = ""
	if RedactSensitiveText(parsed.String(), true, false) != parsed.String() {
		return cyber.Exchange{}, fmt.Errorf("browser URL contains unsupported sensitive data")
	}
	exchange := cyber.Exchange{Method: item.Method, URL: parsed.String(), StatusCode: item.StatusCode, HTTPProtocol: item.Protocol,
		Source: "browser", BrowserSessionID: item.SessionID, BrowserRequestID: item.RequestID,
		HeadersTruncated: item.HeadersIncomplete, RequestTruncated: item.RequestIncomplete, ResponseTruncated: item.ResponseIncomplete, Redacted: redacted}
	names := make([]string, 0, len(item.RequestHeaders))
	contentType := ""
	for name := range item.RequestHeaders {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := item.RequestHeaders[name]
		if strings.EqualFold(name, "Content-Type") {
			contentType, _, _ = mime.ParseMediaType(value)
		}
		if browserSecretField(name) {
			exchange.Redacted = true
			continue
		}
		filtered := RedactSensitiveText(value, true, false)
		exchange.Redacted = exchange.Redacted || filtered != value
		exchange.ReqHeaders += name + ": " + filtered + "\n"
	}
	if item.Request != "" {
		var changed bool
		switch {
		case contentType == "application/json" || strings.HasSuffix(contentType, "+json"):
			exchange.Request, changed, err = redactBrowserJSON(item.Request)
		case contentType == "application/x-www-form-urlencoded":
			exchange.Request, changed, err = redactBrowserValues(item.Request)
		default:
			err = fmt.Errorf("request body type cannot be safely filtered")
		}
		if err != nil {
			exchange.Request, exchange.RequestTruncated, exchange.Redacted = "", true, true
		}
		exchange.Redacted = exchange.Redacted || changed
	}
	if strings.TrimSpace(item.Response) != "" {
		response, _, err := redactBrowserJSON(item.Response)
		if err == nil && !item.ResponseIncomplete {
			exchange.Response = response
		} else {
			exchange.ResponseTruncated = true
		}
	}
	return exchange, nil
}
