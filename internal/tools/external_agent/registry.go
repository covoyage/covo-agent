package externalagent

import (
	"sort"
	"strings"
	"sync"
)

// Registry holds the configured external agent providers. Which providers are
// registered is controlled by the COVO_EXTERNAL_AGENTS environment variable:
//
//	"all"                register every known provider (default)
//	"claude,codex"       register only the listed providers
//	"off" (or "none")    register none; the external_agent tool is not exposed
//
// Regardless of registration, a provider is only usable at call time when its
// CLI binary exists on PATH.
type Registry struct {
	workDir string

	mu        sync.RWMutex
	providers map[string]Provider
	any       bool
}

// NewRegistry builds a registry for workDir (the default working directory
// used for delegated tasks). rawConfig is the value of COVO_EXTERNAL_AGENTS
// ("" means "all").
func NewRegistry(workDir, rawConfig string) *Registry {
	r := &Registry{
		workDir:   workDir,
		providers: make(map[string]Provider),
	}
	r.configure(rawConfig)
	return r
}

func (r *Registry) configure(rawConfig string) {
	cfg := strings.TrimSpace(rawConfig)
	if cfg == "" {
		cfg = "all"
	}
	cfg = strings.ToLower(cfg)

	if cfg == "off" || cfg == "none" || cfg == "" {
		r.any = false
		return
	}

	known := []Provider{
		ClaudeProvider(),
		CodexProvider(),
		OpenCodeProvider(),
	}

	if cfg == "all" {
		for _, p := range known {
			r.providers[p.Name()] = p
		}
		r.any = true
		return
	}

	want := map[string]bool{}
	for _, name := range strings.Split(cfg, ",") {
		if n := strings.TrimSpace(name); n != "" {
			want[n] = true
		}
	}
	for _, p := range known {
		if want[p.Name()] {
			r.providers[p.Name()] = p
		}
	}
	r.any = len(r.providers) > 0
}

// WorkDir returns the default working directory for delegated tasks.
func (r *Registry) WorkDir() string { return r.workDir }

// AnyEnabled reports whether at least one provider is registered.
func (r *Registry) AnyEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.any
}

// Get returns the named provider if registered.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Default returns the first registered provider whose CLI is available on
// PATH. ok is false when no provider can run right now.
func (r *Registry) Default() (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := r.providers[name]
		if _, ok := p.Available(); ok {
			return p, true
		}
	}
	return nil, false
}

// Availability reports the per-provider availability reasons, used to explain
// to the model why delegation is currently impossible.
func (r *Registry) Availability() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]string{}
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		reason, _ := r.providers[name].Available()
		if reason == "" {
			reason = "available"
		}
		out[name] = reason
	}
	return out
}
