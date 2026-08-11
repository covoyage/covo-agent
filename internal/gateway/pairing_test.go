package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

// mockPlatform is a minimal PlatformProvider for testing.
type mockPlatform struct {
	name     string
	sent     []string
	onMsg    func(plugin.IncomingMessage)
	validate error
}

func (m *mockPlatform) Name() string                         { return m.name }
func (m *mockPlatform) Category() plugin.Category            { return plugin.CategoryPlatform }
func (m *mockPlatform) Start(ctx context.Context) error      { return nil }
func (m *mockPlatform) Stop() error                          { return nil }
func (m *mockPlatform) Validate() error                      { return m.validate }
func (m *mockPlatform) OnMessage(cb func(plugin.IncomingMessage)) { m.onMsg = cb }
func (m *mockPlatform) Send(ctx context.Context, channelID, text string) error {
	m.sent = append(m.sent, text)
	return nil
}
func (m *mockPlatform) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	m.sent = append(m.sent, msg.Text)
	return nil
}

// pairingMockAgent is a minimal Agent for testing pairing.
type pairingMockAgent struct {
	response string
}

func (a *pairingMockAgent) Run(ctx context.Context, input string) (string, error) {
	return a.response, nil
}
func (a *pairingMockAgent) Close() {}

func TestPairing_UnapprovedUser_ReceivesCode(t *testing.T) {
	ps := NewPairingStore(t.TempDir())
	plat := &mockPlatform{name: "telegram"}
	gw := New(Config{
		Platforms:    []plugin.PlatformProvider{plat},
		AgentFactory: func(ctx context.Context) (Agent, error) { return &pairingMockAgent{response: "hi"}, nil },
		PairingStore: ps,
	})

	msg := plugin.IncomingMessage{
		Platform:  "telegram",
		ChannelID: "chat1",
		UserID:    "user1",
		UserName:  "Alice",
		Text:      "hello",
		Timestamp: time.Now(),
	}
	gw.handleMessage(context.Background(), plat, msg)

	if len(plat.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(plat.sent))
	}
	if !containsCode(plat.sent[0]) {
		t.Errorf("expected pairing code in message, got: %s", plat.sent[0])
	}
}

func TestPairing_ApprovedUser_PassesThrough(t *testing.T) {
	ps := NewPairingStore(t.TempDir())

	// Manually approve
	ps.mu.Lock()
	if ps.approved["telegram"] == nil {
		ps.approved["telegram"] = make(map[string]bool)
	}
	ps.approved["telegram"]["user1"] = true
	ps.mu.Unlock()

	plat := &mockPlatform{name: "telegram"}
	gw := New(Config{
		Platforms:    []plugin.PlatformProvider{plat},
		AgentFactory: func(ctx context.Context) (Agent, error) { return &pairingMockAgent{response: "hello!"}, nil },
		PairingStore: ps,
	})

	msg := plugin.IncomingMessage{
		Platform:  "telegram",
		ChannelID: "chat1",
		UserID:    "user1",
		UserName:  "Alice",
		Text:      "hello",
		Timestamp: time.Now(),
	}
	gw.handleMessage(context.Background(), plat, msg)

	if len(plat.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(plat.sent))
	}
	if plat.sent[0] != "hello!" {
		t.Errorf("expected agent response 'hello!', got: %s", plat.sent[0])
	}
}

func TestPairing_NilPairingStore_AllowsAll(t *testing.T) {
	plat := &mockPlatform{name: "telegram"}
	gw := New(Config{
		Platforms:    []plugin.PlatformProvider{plat},
		AgentFactory: func(ctx context.Context) (Agent, error) { return &pairingMockAgent{response: "ok"}, nil },
		// No PairingStore
	})

	msg := plugin.IncomingMessage{
		Platform:  "telegram",
		ChannelID: "chat1",
		UserID:    "anyone",
		Text:      "hello",
		Timestamp: time.Now(),
	}
	gw.handleMessage(context.Background(), plat, msg)

	if len(plat.sent) != 1 || plat.sent[0] != "ok" {
		t.Errorf("expected agent response 'ok', got: %v", plat.sent)
	}
}

func TestPairing_RateLimited_NoResponse(t *testing.T) {
	ps := NewPairingStore(t.TempDir())
	plat := &mockPlatform{name: "telegram"}
	gw := New(Config{
		Platforms:    []plugin.PlatformProvider{plat},
		AgentFactory: func(ctx context.Context) (Agent, error) { return &pairingMockAgent{}, nil },
		PairingStore: ps,
	})

	// First request - should get code
	msg1 := plugin.IncomingMessage{
		Platform: "telegram", ChannelID: "c1", UserID: "u1", Text: "hi",
		Timestamp: time.Now(),
	}
	gw.handleMessage(context.Background(), plat, msg1)
	if len(plat.sent) != 1 {
		t.Fatalf("expected 1 message on first request, got %d", len(plat.sent))
	}

	// Second request immediately - should be rate limited (no response)
	plat.sent = nil
	msg2 := plugin.IncomingMessage{
		Platform: "telegram", ChannelID: "c1", UserID: "u1", Text: "hi again",
		Timestamp: time.Now(),
	}
	gw.handleMessage(context.Background(), plat, msg2)
	if len(plat.sent) != 0 {
		t.Errorf("expected 0 messages when rate limited, got %d", len(plat.sent))
	}
}

func TestPairing_Cleanup(t *testing.T) {
	ps := NewPairingStore(t.TempDir())

	// Add a code and manually backdate it
	code, err := ps.RequestCode("telegram", "u1", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	_ = code

	// Backdate the entry
	ps.mu.Lock()
	for i := range ps.pending["telegram"] {
		ps.pending["telegram"][i].CreatedAt = time.Now().Add(-2 * time.Hour)
	}
	ps.mu.Unlock()

	ps.Cleanup()

	pending := ps.ListPending("telegram")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after cleanup, got %d", len(pending))
	}
}

func containsCode(s string) bool {
	// Check if string contains an 8-char code from the alphabet
	for i := 0; i <= len(s)-8; i++ {
		valid := true
		for j := 0; j < 8; j++ {
			c := s[i+j]
			if !((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '9')) {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return false
}
