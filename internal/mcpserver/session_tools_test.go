package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSessionToolProvider_StubsReturnUnavailablePayload(t *testing.T) {
	p := &SessionToolProvider{}
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(context.Context, json.RawMessage) (any, error)
	}{
		{name: "messages_send", fn: p.messagesSend},
		{name: "events_poll", fn: p.eventsPoll},
		{name: "events_wait", fn: p.eventsWait},
		{name: "permissions_list_open", fn: p.permissionsListOpen},
		{name: "permissions_respond", fn: p.permissionsRespond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.fn(ctx, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			m, ok := res.(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any response, got %T", res)
			}
			if got, _ := m["status"].(string); got != "unavailable" {
				t.Fatalf("status = %q, want unavailable", got)
			}
			if got, _ := m["code"].(string); got != "gateway_required" {
				t.Fatalf("code = %q, want gateway_required", got)
			}
			if _, ok := m["message"].(string); !ok {
				t.Fatalf("message missing or not a string: %#v", m["message"])
			}
		})
	}
}
