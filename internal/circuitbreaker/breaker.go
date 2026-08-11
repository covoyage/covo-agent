// Package circuitbreaker implements a sliding-window HTTP circuit breaker.
//
// The breaker trips when sample_count >= min_samples AND error_rate >=
// error_rate_threshold over the live window. Three states: closed (normal),
// open (tripped — requests fail fast), half-open (one trial after cooldown).
package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// State enumerates the circuit breaker states.
type State int

const (
	StateClosed   State = iota // normal operation
	StateOpen                  // tripped — requests fail fast
	StateHalfOpen              // one trial allowed after cooldown
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config controls the circuit breaker behavior.
type Config struct {
	// MinSamples is the minimum number of requests before the breaker
	// can trip (avoids flapping on the first few calls).
	MinSamples int
	// ErrorRateThreshold is the error rate (0.0–1.0) at/above which
	// the breaker trips, provided MinSamples is met.
	ErrorRateThreshold float64
	// WindowSize is the number of requests in the sliding window.
	WindowSize int
	// Cooldown is how long the breaker stays open before allowing a
	// half-open trial.
	Cooldown time.Duration
	// FailureCodes are HTTP status codes counted as failures (e.g. 500, 502, 503).
	// nil/empty means any non-2xx is a failure.
	FailureCodes map[int]struct{}
}

// Preset returns a Config tuned for HTTP server-to-server calls.
func ServerPreset() Config {
	return Config{
		MinSamples:        5,
		ErrorRateThreshold: 0.5,
		WindowSize:        30,
		Cooldown:          10 * time.Second,
		FailureCodes:      defaultFailureCodes(),
	}
}

// Preset returns a Config tuned for client-to-API calls (more tolerant).
func ClientPreset() Config {
	return Config{
		MinSamples:        10,
		ErrorRateThreshold: 0.6,
		WindowSize:        50,
		Cooldown:          30 * time.Second,
		FailureCodes:      defaultFailureCodes(),
	}
}

func defaultFailureCodes() map[int]struct{} {
	return map[int]struct{}{
		500: {}, 502: {}, 503: {}, 504: {},
	}
}

// ErrCircuitOpen is returned when the breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Breaker is a single-endpoint circuit breaker.
type Breaker struct {
	mu sync.Mutex

	config Config
	state  State

	// sliding window: ring buffer of success/failure booleans
	window  []bool
	head    int
	count   int
	failure int

	// state transition timestamps
	openedAt time.Time
	// half-open trial tracking
	trialInFlight bool
	// onTransition is called when the breaker changes state (e.g. closed→open).
	// The callback receives the new state and is called with the mutex held.
	onTransition func(newState State)
}

// New creates a Breaker with the given Config.
func New(cfg Config) *Breaker {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 30
	}
	if cfg.MinSamples <= 0 {
		cfg.MinSamples = 5
	}
	if cfg.ErrorRateThreshold <= 0 {
		cfg.ErrorRateThreshold = 0.5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 10 * time.Second
	}
	return &Breaker{
		config: cfg,
		state:  StateClosed,
		window: make([]bool, cfg.WindowSize),
	}
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpen()
	return b.state
}

// Allow checks whether a request should proceed. If the breaker is open
// and cooldown hasn't elapsed, it returns ErrCircuitOpen. If cooldown has
// elapsed, it transitions to half-open and allows one trial.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.maybeHalfOpen()

	switch b.state {
	case StateOpen:
		return ErrCircuitOpen
	case StateHalfOpen:
		if b.trialInFlight {
			return ErrCircuitOpen
		}
		b.trialInFlight = true
		return nil
	default:
		return nil
	}
}

// RecordSuccess records a successful request.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	prevState := b.state
	switch b.state {
	case StateHalfOpen:
		// successful trial → close
		b.state = StateClosed
		b.trialInFlight = false
		b.resetWindow()
	case StateClosed:
		b.record(true)
	}
	if b.state != prevState && b.onTransition != nil {
		b.onTransition(b.state)
	}
}

// RecordFailure records a failed request.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	prevState := b.state
	switch b.state {
	case StateHalfOpen:
		// failed trial → re-open
		b.state = StateOpen
		b.openedAt = time.Now()
		b.trialInFlight = false
	case StateClosed:
		b.record(false)
		if b.shouldTrip() {
			b.state = StateOpen
			b.openedAt = time.Now()
		}
	}
	if b.state != prevState && b.onTransition != nil {
		b.onTransition(b.state)
	}
}

// maybeHalfOpen transitions from open to half-open if cooldown has elapsed.
func (b *Breaker) maybeHalfOpen() {
	if b.state == StateOpen && time.Since(b.openedAt) >= b.config.Cooldown {
		b.state = StateHalfOpen
		b.trialInFlight = false
	}
}

// record pushes a result into the sliding window.
func (b *Breaker) record(success bool) {
	if b.count >= b.config.WindowSize {
		// window full: evict oldest
		old := b.window[b.head]
		if !old {
			b.failure--
		}
	}

	b.window[b.head] = success
	b.head = (b.head + 1) % b.config.WindowSize
	if b.count < b.config.WindowSize {
		b.count++
	}
	if !success {
		b.failure++
	}
}

// shouldTrip checks if the error rate exceeds the threshold.
func (b *Breaker) shouldTrip() bool {
	if b.count < b.config.MinSamples {
		return false
	}
	errorRate := float64(b.failure) / float64(b.count)
	return errorRate >= b.config.ErrorRateThreshold
}

func (b *Breaker) resetWindow() {
	b.window = make([]bool, b.config.WindowSize)
	b.head = 0
	b.count = 0
	b.failure = 0
}

// Stats returns a snapshot of breaker statistics.
type Stats struct {
	State        State   `json:"state"`
	Count        int     `json:"count"`
	Failures     int     `json:"failures"`
	ErrorRate    float64 `json:"error_rate"`
	OpenedAgo    string  `json:"opened_ago,omitempty"`
}

// Stats returns a snapshot of the current breaker state.
func (b *Breaker) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.maybeHalfOpen()

	s := Stats{State: b.state, Count: b.count, Failures: b.failure}
	if b.count > 0 {
		s.ErrorRate = float64(b.failure) / float64(b.count)
	}
	if b.state == StateOpen || b.state == StateHalfOpen {
		s.OpenedAgo = time.Since(b.openedAt).Round(time.Second).String()
	}
	return s
}

// ---------------------------------------------------------------------------
// Registry — manages multiple breakers by endpoint/host
// ---------------------------------------------------------------------------

// Registry holds named circuit breakers.
type Registry struct {
	mu       sync.Mutex
	breakers map[string]*Breaker
	defaults Config
	onTrip   func(key string, stats Stats) // called when a breaker transitions to open
}

// NewRegistry creates a registry. All breakers created via GetOrCreate
// will use the provided default Config.
func NewRegistry(defaults Config) *Registry {
	return &Registry{
		breakers: make(map[string]*Breaker),
		defaults: defaults,
	}
}

// SetOnTrip sets a callback invoked when any breaker in this registry
// transitions from closed/half-open to the open state. The callback
// receives the breaker key (typically the hostname) and the breaker
// stats at trip time.
func (r *Registry) SetOnTrip(fn func(key string, stats Stats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onTrip = fn
}

// GetOrCreate returns the breaker for the given key (e.g. "api.openai.com"),
// creating one with the default Config if it doesn't exist.
func (r *Registry) GetOrCreate(key string) *Breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.breakers[key]
	if !ok {
		b = New(r.defaults)
		// Wire the onTransition callback to fire onTrip on state change to open
		capturedKey := key
		b.onTransition = func(newState State) {
			if newState == StateOpen {
				r.mu.Lock()
				onTrip := r.onTrip
				r.mu.Unlock()
				if onTrip != nil {
					onTrip(capturedKey, b.Stats())
				}
			}
		}
		r.breakers[key] = b
	}
	return b
}

// All returns a map of all breaker stats.
func (r *Registry) All() map[string]Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Stats, len(r.breakers))
	for k, b := range r.breakers {
		out[k] = b.Stats()
	}
	return out
}

// ---------------------------------------------------------------------------
// HTTP RoundTripper integration
// ---------------------------------------------------------------------------

// RoundTripper wraps an http.RoundTripper with per-host circuit breaking.
type RoundTripper struct {
	inner     http.RoundTripper
	registry  *Registry
	breakerFn func(*http.Request) string
}

// NewRoundTripper wraps inner with circuit breaking. The breakerFn maps
// a request to a breaker key (typically the host). If nil, r.Host is used.
func NewRoundTripper(inner http.RoundTripper, registry *Registry, breakerFn func(*http.Request) string) *RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &RoundTripper{
		inner:     inner,
		registry:  registry,
		breakerFn: breakerFn,
	}
}

func (rt *RoundTripper) key(r *http.Request) string {
	if rt.breakerFn != nil {
		return rt.breakerFn(r)
	}
	return r.Host
}

func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	key := rt.key(req)
	b := rt.registry.GetOrCreate(key)

	if err := b.Allow(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, key)
	}

	resp, err := rt.inner.RoundTrip(req)
	if err != nil {
		b.RecordFailure()
		return nil, err
	}

	if rt.isFailure(resp) {
		b.RecordFailure()
	} else {
		b.RecordSuccess()
	}
	return resp, nil
}

func (rt *RoundTripper) isFailure(resp *http.Response) bool {
	cfg := rt.registry.defaults
	if len(cfg.FailureCodes) == 0 {
		return resp.StatusCode < 200 || resp.StatusCode >= 300
	}
	_, isFailure := cfg.FailureCodes[resp.StatusCode]
	return isFailure
}

// WithCircuitBreaker returns an *http.Client whose Transport is wrapped
// with circuit breaking using the given Config.
func WithCircuitBreaker(cfg Config) *http.Client {
	registry := NewRegistry(cfg)
	return &http.Client{
		Transport: NewRoundTripper(http.DefaultTransport, registry, nil),
	}
}

// Do wraps a context-aware HTTP call with circuit breaking.
func Do(ctx context.Context, client *http.Client, registry *Registry, req *http.Request) (*http.Response, error) {
	key := req.Host
	b := registry.GetOrCreate(key)

	if err := b.Allow(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, key)
	}

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		b.RecordFailure()
		return nil, err
	}

	cfg := registry.defaults
	isFailure := false
	if len(cfg.FailureCodes) == 0 {
		isFailure = resp.StatusCode < 200 || resp.StatusCode >= 300
	} else {
		_, isFailure = cfg.FailureCodes[resp.StatusCode]
	}

	if isFailure {
		b.RecordFailure()
	} else {
		b.RecordSuccess()
	}
	return resp, nil
}
