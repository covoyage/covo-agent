package cyber

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type OASTObservation struct {
	Name          string    `json:"name"`
	RemoteAddress string    `json:"remote_address,omitempty"`
	ProviderTime  string    `json:"provider_time,omitempty"`
	ReceivedAt    time.Time `json:"received_at"`
}

type OASTSession struct {
	ID              string            `json:"id"`
	Domain          string            `json:"domain"`
	Provider        string            `json:"provider"`
	FindingID       string            `json:"finding_id,omitempty"`
	ExchangeID      string            `json:"exchange_id,omitempty"`
	SourceSessionID string            `json:"source_session_id"`
	CreatedAt       time.Time         `json:"created_at"`
	ExpiresAt       time.Time         `json:"expires_at"`
	Observations    []OASTObservation `json:"observations"`
}

type oastState struct {
	OASTSession
	Cookies []*http.Cookie `json:"cookies"`
}

func (c *Catalog) oastPath() string { return filepath.Join(c.dir, "oast.json") }

func ValidOASTDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return true
}

func (c *Catalog) BeginOAST(provider, baseDomain, source, findingID, exchangeID string, cookies []*http.Cookie) (OASTSession, error) {
	parsed, err := url.Parse(provider)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return OASTSession{}, fmt.Errorf("OAST provider must be an HTTPS URL without credentials, query or fragment")
	}
	baseDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(baseDomain), "."))
	if !ValidOASTDomain(baseDomain) || source == "" || len(cookies) == 0 || len(cookies) > 16 {
		return OASTSession{}, fmt.Errorf("OAST allocation requires a domain, source session and isolated provider cookies")
	}
	for _, cookie := range cookies {
		if cookie == nil || cookie.Valid() != nil || len(cookie.Name)+len(cookie.Value) > 4096 {
			return OASTSession{}, fmt.Errorf("invalid or oversized OAST provider cookie")
		}
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if findingID != "" {
		findings, err := c.loadFindingsLocked()
		if err != nil {
			return OASTSession{}, err
		}
		found := false
		for _, finding := range findings {
			found = found || finding.ID == findingID
		}
		if !found {
			return OASTSession{}, fmt.Errorf("finding not found")
		}
	}
	if exchangeID != "" {
		exchanges, err := c.loadExchangesLocked()
		if err != nil {
			return OASTSession{}, err
		}
		found := false
		for _, exchange := range exchanges {
			found = found || exchange.ID == exchangeID
		}
		if !found {
			return OASTSession{}, fmt.Errorf("exchange not found")
		}
	}
	var states []oastState
	if err := c.readJSON(c.oastPath(), &states); err != nil {
		return OASTSession{}, err
	}
	if len(states) >= 512 {
		return OASTSession{}, fmt.Errorf("OAST session capacity reached (512)")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return OASTSession{}, err
	}
	label := hex.EncodeToString(nonce[:])
	now := time.Now().UTC()
	session := OASTSession{ID: "oast-" + label, Domain: label + "." + baseDomain, Provider: provider, FindingID: findingID, ExchangeID: exchangeID, SourceSessionID: source, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Observations: []OASTObservation{}}
	for _, cookie := range cookies {
		if !cookie.Expires.IsZero() && cookie.Expires.Before(session.ExpiresAt) {
			session.ExpiresAt = cookie.Expires
		}
	}
	if !now.Before(session.ExpiresAt) {
		return OASTSession{}, fmt.Errorf("provider returned expired session cookies")
	}
	if !ValidOASTDomain(session.Domain) {
		return OASTSession{}, fmt.Errorf("allocated OAST domain exceeds DNS limits")
	}
	states = append(states, oastState{OASTSession: session, Cookies: cookies})
	return session, c.writeJSON(c.oastPath(), states)
}

func (c *Catalog) OASTCredentials(id, provider string) (OASTSession, []*http.Cookie, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var states []oastState
	if err := c.readJSON(c.oastPath(), &states); err != nil {
		return OASTSession{}, nil, err
	}
	for _, state := range states {
		if state.ID == id {
			if state.Provider != provider || !time.Now().Before(state.ExpiresAt) {
				return OASTSession{}, nil, fmt.Errorf("OAST session expired or provider changed")
			}
			return state.OASTSession, state.Cookies, nil
		}
	}
	return OASTSession{}, nil, fmt.Errorf("OAST session not found")
}

func (c *Catalog) RecordOAST(id string, observations []OASTObservation, updatedCookies ...[]*http.Cookie) (OASTSession, error) {
	if len(observations) > 100 {
		return OASTSession{}, fmt.Errorf("OAST observation budget exceeded (100)")
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var states []oastState
	if err := c.readJSON(c.oastPath(), &states); err != nil {
		return OASTSession{}, err
	}
	for index := range states {
		state := &states[index]
		if state.ID != id {
			continue
		}
		if !time.Now().Before(state.ExpiresAt) {
			return OASTSession{}, fmt.Errorf("OAST session expired")
		}
		for _, observation := range observations {
			observation.Name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(observation.Name), "."))
			if !ValidOASTDomain(observation.Name) || observation.Name != state.Domain && !strings.HasSuffix(observation.Name, "."+state.Domain) {
				continue
			}
			if observation.RemoteAddress != "" && net.ParseIP(observation.RemoteAddress) == nil {
				return OASTSession{}, fmt.Errorf("invalid OAST remote address")
			}
			if len(observation.ProviderTime) > 128 {
				return OASTSession{}, fmt.Errorf("invalid OAST provider timestamp")
			}
			duplicate := false
			for _, existing := range state.Observations {
				duplicate = duplicate || existing.Name == observation.Name && existing.RemoteAddress == observation.RemoteAddress && existing.ProviderTime == observation.ProviderTime
			}
			if duplicate {
				continue
			}
			if len(state.Observations) >= 100 {
				return OASTSession{}, fmt.Errorf("OAST observation capacity reached (100)")
			}
			observation.ReceivedAt = time.Now().UTC()
			state.Observations = append(state.Observations, observation)
		}
		if len(updatedCookies) > 0 {
			cookies := updatedCookies[0]
			if len(cookies) == 0 || len(cookies) > 16 {
				return OASTSession{}, fmt.Errorf("invalid rotated OAST cookies")
			}
			for _, cookie := range cookies {
				if cookie == nil || cookie.Valid() != nil || len(cookie.Name)+len(cookie.Value) > 4096 {
					return OASTSession{}, fmt.Errorf("invalid rotated OAST cookie")
				}
				if !cookie.Expires.IsZero() && cookie.Expires.Before(state.ExpiresAt) {
					state.ExpiresAt = cookie.Expires
				}
			}
			state.Cookies = cookies
		}
		return state.OASTSession, c.writeJSON(c.oastPath(), states)
	}
	return OASTSession{}, fmt.Errorf("OAST session not found")
}

func (c *Catalog) ListOAST() ([]OASTSession, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	var states []oastState
	if err := c.readJSON(c.oastPath(), &states); err != nil {
		return nil, err
	}
	sessions := make([]OASTSession, 0, len(states))
	for _, state := range states {
		sessions = append(sessions, state.OASTSession)
	}
	return sessions, nil
}
