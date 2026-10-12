package cyber

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

type ObservedParameter struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Redacted bool   `json:"redacted,omitempty"`
}

type RequestObservation struct {
	OperationKey string              `json:"operation_key"`
	ExchangeID   string              `json:"exchange_id"`
	Identity     string              `json:"identity,omitempty"`
	Role         string              `json:"role,omitempty"`
	Tenant       string              `json:"tenant,omitempty"`
	Resource     string              `json:"resource,omitempty"`
	Parameters   []ObservedParameter `json:"parameters"`
	Complete     bool                `json:"complete"`
}

type OperationContext struct {
	Shape        OperationShape       `json:"shape"`
	Revision     string               `json:"revision"`
	Observations []RequestObservation `json:"observations"`
	Total        int                  `json:"total"`
	Truncated    bool                 `json:"truncated"`
}

func (c *Catalog) requestObservationsPath() string {
	return filepath.Join(c.dir, "request_observations.json")
}

func requestObservation(ex Exchange, parsed *url.URL, shape OperationShape) RequestObservation {
	observation := RequestObservation{
		OperationKey: shape.Key, ExchangeID: ex.ID, Identity: ex.Identity, Role: ex.Role,
		Tenant: ex.Tenant, Resource: ex.Resource, Complete: shape.Complete, Parameters: []ObservedParameter{},
	}
	add := func(location, name, value string) {
		if len(observation.Parameters) >= 32 || len(name) > 256 {
			observation.Complete = false
			return
		}
		if sensitiveObservationName(name) {
			return
		}
		retained := !ex.Redacted && identifierValue(value)
		parameter := ObservedParameter{Location: location, Name: name, Redacted: !retained}
		if retained {
			parameter.Value = value
		}
		observation.Parameters = append(observation.Parameters, parameter)
	}
	for index, segment := range strings.Split(parsed.EscapedPath(), "/") {
		if identifierValue(segment) {
			add("path", fmt.Sprintf("/%d", index), segment)
		}
	}
	query, _ := url.ParseQuery(parsed.RawQuery)
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range query[name] {
			add("query", name, value)
		}
	}
	var body any
	decoder := json.NewDecoder(strings.NewReader(ex.Request))
	decoder.UseNumber()
	if json.Valid([]byte(ex.Request)) && decoder.Decode(&body) == nil {
		if object, ok := body.(map[string]any); ok {
			switch shape.Protocol {
			case "graphql":
				body = object["variables"]
			case "jsonrpc":
				body = object["params"]
			}
		}
		count := 0
		var walk func(any, string, int)
		walk = func(value any, path string, depth int) {
			count++
			if depth > 8 || count > 128 {
				observation.Complete = false
				return
			}
			switch typed := value.(type) {
			case map[string]any:
				keys := make([]string, 0, len(typed))
				for key := range typed {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if !sensitiveObservationName(key) {
						escaped := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
						walk(typed[key], path+"/"+escaped, depth+1)
					}
				}
			case []any:
				for index, child := range typed {
					walk(child, fmt.Sprintf("%s/%d", path, index), depth+1)
				}
			default:
				if path != "" {
					add("body", path, fmt.Sprint(value))
				}
			}
		}
		walk(body, "", 0)
	} else if form, err := url.ParseQuery(ex.Request); err == nil && strings.Contains(ex.Request, "=") {
		keys := make([]string, 0, len(form))
		for key := range form {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, value := range form[key] {
				add("form", key, value)
			}
		}
	}
	return observation
}

func sensitiveObservationName(name string) bool {
	normalized := strings.ToLower(name)
	for _, fragment := range []string{"auth", "token", "secret", "password", "passwd", "cookie", "session", "credential", "key", "csrf", "xsrf", "nonce", "bearer", "code", "otp", "pin", "email", "phone"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func observationFingerprint(observation RequestObservation) string {
	observation.ExchangeID = ""
	data, _ := json.Marshal(observation)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func observationRevision(items []RequestObservation, operationKey string) string {
	fingerprints := []string{}
	for _, item := range items {
		if item.OperationKey == operationKey {
			fingerprints = append(fingerprints, observationFingerprint(item))
		}
	}
	sort.Strings(fingerprints)
	data, _ := json.Marshal(fingerprints)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (c *Catalog) observeRequestLocked(ex Exchange, parsed *url.URL, shape OperationShape) (string, error) {
	var items []RequestObservation
	if err := c.readJSON(c.requestObservationsPath(), &items); err != nil {
		return "", err
	}
	observation := requestObservation(ex, parsed, shape)
	fingerprint := observationFingerprint(observation)
	updated := make([]RequestObservation, 0, len(items)+1)
	duplicate := false
	for _, item := range items {
		if item.ExchangeID == ex.ID {
			continue
		}
		duplicate = duplicate || observationFingerprint(item) == fingerprint
		updated = append(updated, item)
	}
	if !duplicate {
		if len(updated) >= 500 {
			return "", fmt.Errorf("request observation ledger is full (500 records)")
		}
		updated = append(updated, observation)
	}
	if err := c.writeJSON(c.requestObservationsPath(), updated); err != nil {
		return "", err
	}
	return observationRevision(updated, shape.Key), nil
}

func (c *Catalog) RequestContext(id string) (OperationContext, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	exchanges, err := c.loadExchangesLocked()
	if err != nil {
		return OperationContext{}, err
	}
	for _, ex := range exchanges {
		if ex.ID != id {
			continue
		}
		parsed, err := url.Parse(ex.URL)
		if err != nil || parsed.Hostname() == "" {
			return OperationContext{}, fmt.Errorf("invalid exchange url")
		}
		shape := operationForExchange(ex, parsed)
		var items []RequestObservation
		if err := c.readJSON(c.requestObservationsPath(), &items); err != nil {
			return OperationContext{}, err
		}
		result := OperationContext{Shape: shape, Revision: observationRevision(items, shape.Key), Observations: []RequestObservation{}}
		for _, item := range items {
			if item.OperationKey == shape.Key {
				result.Observations = append(result.Observations, item)
				result.Total++
			}
		}
		if len(result.Observations) > 20 {
			result.Observations = result.Observations[len(result.Observations)-20:]
			result.Truncated = true
		}
		return result, nil
	}
	return OperationContext{}, fmt.Errorf("exchange %q not found", id)
}
