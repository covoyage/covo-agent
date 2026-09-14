package nextcloudtalk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func testAdapter(serverURL string) *Adapter {
	a := New()
	a.baseURL = serverURL
	a.username = "me"
	a.password = "secret"
	return a
}

func parseBasicAuth(header string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}
	user, pass, ok = strings.Cut(string(decoded), ":")
	return
}

func TestValidate(t *testing.T) {
	a := New()
	if err := a.Validate(); err == nil {
		t.Fatal("expected validation error with empty config")
	}
	a.baseURL = "http://example.com"
	a.username = "u"
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for missing password")
	}
	a.password = "p"
	if err := a.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRooms(t *testing.T) {
	a := New()
	a.roomsEnv = "abc, def ,,ghi"
	got := a.rooms()
	if len(got) != 3 || got[0] != "abc" || got[1] != "def" || got[2] != "ghi" {
		t.Errorf("rooms() = %v, want [abc def ghi]", got)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	var gotMessage, gotAuth, gotOCS, gotType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != chatBasePath+"/room1" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotOCS = r.Header.Get("OCS-APIRequest")
		gotType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		gotMessage = vals.Get("message")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.SendMessage(context.Background(), "room1", plugin.OutgoingMessage{Text: "hello"}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	user, pass, ok := parseBasicAuth(gotAuth)
	if !ok || user != "me" || pass != "secret" {
		t.Errorf("basic auth = %q/%q ok=%v", user, pass, ok)
	}
	if gotOCS != "true" {
		t.Errorf("OCS-APIRequest = %q", gotOCS)
	}
	if !strings.Contains(gotType, "application/x-www-form-urlencoded") {
		t.Errorf("content type = %q", gotType)
	}
	if gotMessage != "hello" {
		t.Errorf("message = %q", gotMessage)
	}
}

func TestSendMessageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.SendMessage(context.Background(), "room1", plugin.OutgoingMessage{Text: "hi"}); err == nil {
		t.Fatal("expected error on HTTP 403")
	}
}

func TestFetchNewMessages(t *testing.T) {
	var gotQuery url.Values
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		resp := map[string]any{
			"ocs": map[string]any{
				"meta": map[string]any{"status": "ok"},
				"data": []map[string]any{
					{"id": 101, "actorId": "alice", "actorDisplayName": "Alice", "message": "hello", "messageType": "comment"},
					{"id": 102, "actorId": "me", "actorDisplayName": "Me", "message": "self echo", "messageType": "comment"},
					{"id": 103, "actorId": "alice", "message": "joined", "messageType": "system"},
				},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	msgs, lastID, err := a.fetchNewMessages(context.Background(), "room1", 0, true)
	if err != nil {
		t.Fatalf("fetchNewMessages failed: %v", err)
	}

	if gotPath != chatBasePath+"/room1" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery.Get("lookIntoFuture") != "1" || gotQuery.Get("timeout") != "30" || gotQuery.Get("lastKnownMessageId") != "" {
		t.Errorf("query = %v", gotQuery)
	}
	if lastID != 103 {
		t.Errorf("lastID = %d, want 103", lastID)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 forwarded message, got %d: %+v", len(msgs), msgs)
	}
	in := msgs[0]
	if in.Platform != PlatformName || in.ChannelID != "room1" || in.UserID != "alice" || in.UserName != "Alice" || in.Text != "hello" {
		t.Errorf("unexpected message: %+v", in)
	}

	// Second round with cursor.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("lastKnownMessageId") != "103" {
			http.Error(w, "missing cursor", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ocs":{"data":[]}}`))
	})
	_, lastID, err = a.fetchNewMessages(context.Background(), "room1", 103, true)
	if err != nil || lastID != 103 {
		t.Errorf("cursor round: lastID=%d err=%v", lastID, err)
	}
}

func TestFetchNewMessagesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if _, _, err := a.fetchNewMessages(context.Background(), "room1", 0, true); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestStartCapabilitiesCheck(t *testing.T) {
	var gotOCS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == capabilitiesPath {
			gotOCS = r.Header.Get("OCS-APIRequest")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	a.roomsEnv = "" // no polling goroutines
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if gotOCS != "true" {
		t.Errorf("OCS-APIRequest = %q", gotOCS)
	}
	if err := a.Start(context.Background()); err == nil {
		t.Error("second Start should fail with already running")
	}
	if err := a.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

func TestStartCapabilitiesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := testAdapter(srv.URL)
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("expected Start to fail on unauthorized capabilities")
	}
}
