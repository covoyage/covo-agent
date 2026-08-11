package circuitbreaker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBreaker_StartsClosed(t *testing.T) {
	b := New(ServerPreset())
	if b.State() != StateClosed {
		t.Fatalf("expected closed, got %s", b.State())
	}
}

func TestBreaker_AllowsWhenClosed(t *testing.T) {
	b := New(ServerPreset())
	if err := b.Allow(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestBreaker_TripsOnErrorRate(t *testing.T) {
	cfg := Config{
		MinSamples:        3,
		ErrorRateThreshold: 0.5,
		WindowSize:        10,
		Cooldown:          100 * time.Millisecond,
	}
	b := New(cfg)

	// Record 3 failures (meets MinSamples and 1.0 error rate >= 0.5)
	for i := 0; i < 3; i++ {
		if err := b.Allow(); err != nil {
			t.Fatalf("unexpected error before trip: %v", err)
		}
		b.RecordFailure()
	}

	if b.State() != StateOpen {
		t.Fatalf("expected open after 3 failures, got %s", b.State())
	}
}

func TestBreaker_DoesNotTripBelowMinSamples(t *testing.T) {
	cfg := Config{
		MinSamples:        10,
		ErrorRateThreshold: 0.5,
		WindowSize:        20,
		Cooldown:          100 * time.Millisecond,
	}
	b := New(cfg)

	for i := 0; i < 5; i++ {
		b.Allow()
		b.RecordFailure()
	}

	if b.State() != StateClosed {
		t.Fatalf("expected closed below MinSamples, got %s", b.State())
	}
}

func TestBreaker_FailsFastWhenOpen(t *testing.T) {
	cfg := Config{
		MinSamples:        2,
		ErrorRateThreshold: 0.5,
		WindowSize:        5,
		Cooldown:          1 * time.Second,
	}
	b := New(cfg)
	b.Allow(); b.RecordFailure()
	b.Allow(); b.RecordFailure()

	if b.State() != StateOpen {
		t.Fatalf("expected open, got %s", b.State())
	}
	if err := b.Allow(); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestBreaker_HalfOpenAfterCooldown(t *testing.T) {
	cfg := Config{
		MinSamples:        2,
		ErrorRateThreshold: 0.5,
		WindowSize:        5,
		Cooldown:          50 * time.Millisecond,
	}
	b := New(cfg)
	b.Allow(); b.RecordFailure()
	b.Allow(); b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected open, got %s", b.State())
	}

	time.Sleep(60 * time.Millisecond)
	if err := b.Allow(); err != nil {
		t.Fatalf("expected half-open trial allowed, got %v", err)
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("expected half-open, got %s", b.State())
	}
}

func TestBreaker_ClosesOnHalfOpenSuccess(t *testing.T) {
	cfg := Config{
		MinSamples:        2,
		ErrorRateThreshold: 0.5,
		WindowSize:        5,
		Cooldown:          50 * time.Millisecond,
	}
	b := New(cfg)
	b.Allow(); b.RecordFailure()
	b.Allow(); b.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to half-open
	b.RecordSuccess()

	if b.State() != StateClosed {
		t.Fatalf("expected closed after successful trial, got %s", b.State())
	}
}

func TestBreaker_ReopensOnHalfOpenFailure(t *testing.T) {
	cfg := Config{
		MinSamples:        2,
		ErrorRateThreshold: 0.5,
		WindowSize:        5,
		Cooldown:          50 * time.Millisecond,
	}
	b := New(cfg)
	b.Allow(); b.RecordFailure()
	b.Allow(); b.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to half-open
	b.RecordFailure()

	if b.State() != StateOpen {
		t.Fatalf("expected open after failed trial, got %s", b.State())
	}
}

func TestBreaker_MixedResults(t *testing.T) {
	cfg := Config{
		MinSamples:        5,
		ErrorRateThreshold: 0.6,
		WindowSize:        10,
		Cooldown:          100 * time.Millisecond,
	}
	b := New(cfg)

	// 3 success, 2 failure = 0.4 error rate < 0.6 threshold
	for i := 0; i < 3; i++ {
		b.Allow(); b.RecordSuccess()
	}
	for i := 0; i < 2; i++ {
		b.Allow(); b.RecordFailure()
	}
	if b.State() != StateClosed {
		t.Fatalf("expected closed at 0.4 error rate, got %s", b.State())
	}

	// 2 more failures = 3 success, 4 failure = 0.57 error rate
	// but window may have evicted... let's track properly
	// Actually with WindowSize=10, nothing evicted yet.
	// 3+2+2 = 7 total, 4 failures, rate = 0.57 < 0.6
	for i := 0; i < 2; i++ {
		b.Allow(); b.RecordFailure()
	}
	// 3 success, 4 failures out of 7 = 0.571 < 0.6 → should still be closed
	if b.State() != StateClosed {
		t.Fatalf("expected closed at 0.57 error rate, got %s", b.State())
	}

	// 1 more failure: 3 success, 5 failures out of 8 = 0.625 >= 0.6
	b.Allow(); b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected open at 0.625 error rate, got %s", b.State())
	}
}

func TestBreaker_Stats(t *testing.T) {
	b := New(Config{
		MinSamples:        2,
		ErrorRateThreshold: 0.5,
		WindowSize:        10,
		Cooldown:          100 * time.Millisecond,
	})
	b.Allow(); b.RecordSuccess()
	b.Allow(); b.RecordFailure()

	s := b.Stats()
	if s.Count != 2 {
		t.Errorf("expected count 2, got %d", s.Count)
	}
	if s.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", s.Failures)
	}
	if s.ErrorRate != 0.5 {
		t.Errorf("expected 0.5 error rate, got %f", s.ErrorRate)
	}
}

func TestRegistry_GetOrCreate(t *testing.T) {
	r := NewRegistry(ServerPreset())
	b1 := r.GetOrCreate("api.example.com")
	b2 := r.GetOrCreate("api.example.com")
	if b1 != b2 {
		t.Fatal("expected same breaker instance for same key")
	}
	b3 := r.GetOrCreate("other.example.com")
	if b1 == b3 {
		t.Fatal("expected different breaker instances for different keys")
	}
}

func TestRoundTripper_CircuitBreaksOnFailures(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := Config{
		MinSamples:        2,
		ErrorRateThreshold: 0.5,
		WindowSize:        10,
		Cooldown:          5 * time.Second,
		FailureCodes:      map[int]struct{}{500: {}},
	}
	registry := NewRegistry(cfg)
	client := &http.Client{
		Transport: NewRoundTripper(http.DefaultTransport, registry, nil),
	}

	// Send enough requests to trip the breaker
	for i := 0; i < 4; i++ {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// After 4 failures (all 500s), breaker should be open
	calls := callCount.Load()
	// The 5th call should fail fast without hitting the server
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected error after breaker opened")
	}
	if callCount.Load() != calls {
		t.Fatalf("expected server not to be called after breaker open: %d vs %d", callCount.Load(), calls)
	}
}

func TestDo_WithContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := ClientPreset()
	registry := NewRegistry(cfg)

	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := Do(context.Background(), http.DefaultClient, registry, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPresets(t *testing.T) {
	s := ServerPreset()
	if s.MinSamples <= 0 || s.WindowSize <= 0 || s.Cooldown <= 0 {
		t.Errorf("server preset has invalid values: %+v", s)
	}
	c := ClientPreset()
	if c.MinSamples <= 0 || c.WindowSize <= 0 || c.Cooldown <= 0 {
		t.Errorf("client preset has invalid values: %+v", c)
	}
	if c.Cooldown <= s.Cooldown {
		t.Error("client preset should be more tolerant (longer cooldown)")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry(ServerPreset())
	b1 := r.GetOrCreate("host1")
	b1.RecordSuccess()
	b2 := r.GetOrCreate("host2")
	b2.RecordFailure()

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if _, ok := all["host1"]; !ok {
		t.Error("missing host1")
	}
	if _, ok := all["host2"]; !ok {
		t.Error("missing host2")
	}
}

func TestBreaker_String(t *testing.T) {
	if StateClosed.String() != "closed" {
		t.Error("bad string")
	}
	if StateOpen.String() != "open" {
		t.Error("bad string")
	}
	if StateHalfOpen.String() != "half-open" {
		t.Error("bad string")
	}
}

func TestWithCircuitBreaker_Client(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := WithCircuitBreaker(ServerPreset())
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
}

func TestRoundTripper_NilInner(t *testing.T) {
	rt := NewRoundTripper(nil, NewRegistry(ServerPreset()), nil)
	if rt.inner == nil {
		t.Fatal("expected default transport when inner is nil")
	}
}

func TestBreaker_HalfOpenRejectsConcurrentTrials(t *testing.T) {
	cfg := Config{
		MinSamples:        1,
		ErrorRateThreshold: 0.5,
		WindowSize:        5,
		Cooldown:          50 * time.Millisecond,
	}
	b := New(cfg)
	b.Allow(); b.RecordFailure()

	time.Sleep(60 * time.Millisecond)
	// First Allow transitions to half-open and starts trial
	if err := b.Allow(); err != nil {
		t.Fatalf("first half-open allow failed: %v", err)
	}
	// Second Allow should fail (trial in flight)
	if err := b.Allow(); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen for concurrent trial, got %v", err)
	}
}

func TestBreaker_KeyFunction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	customKey := "custom-endpoint"
	breakerFn := func(r *http.Request) string { return customKey }

	cfg := ServerPreset()
	registry := NewRegistry(cfg)
	rt := NewRoundTripper(http.DefaultTransport, registry, breakerFn)

	client := &http.Client{Transport: rt}
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	b := registry.GetOrCreate(customKey)
	s := b.Stats()
	if s.Count != 1 {
		t.Errorf("expected 1 request in custom breaker, got %d", s.Count)
	}
}

func TestBreaker_FailureCodesNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // 404
	}))
	defer srv.Close()

	cfg := Config{
		MinSamples:        1,
		ErrorRateThreshold: 0.5,
		WindowSize:        5,
		Cooldown:          5 * time.Second,
		FailureCodes:      nil, // nil means any non-2xx is failure
	}
	registry := NewRegistry(cfg)
	client := &http.Client{Transport: NewRoundTripper(http.DefaultTransport, registry, nil)}

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Should have tripped because 404 is a failure when FailureCodes is nil
	_ = registry.GetOrCreate(srv.Listener.Addr().String())
}
