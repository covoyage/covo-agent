package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestUploadDocumentMultipart(t *testing.T) {
	var gotChatID string
	var gotFilename string
	var gotContent []byte
	var gotCaption string
	var gotContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotChatID = r.FormValue("chat_id")
		gotCaption = r.FormValue("caption")
		file, header, err := r.FormFile("document")
		if err != nil {
			t.Errorf("form file document: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		gotFilename = header.Filename
		gotContent, _ = io.ReadAll(file)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()

	adapter := &Adapter{
		token:      "test-token",
		apiBase:    server.URL,
		httpClient: server.Client(),
	}

	media := &plugin.MediaInfo{
		Type:     "document",
		Data:     []byte("hello document"),
		FileName: "report.pdf",
		Caption:  "monthly report",
	}
	if err := adapter.uploadDocument(context.Background(), 12345, media); err != nil {
		t.Fatalf("uploadDocument: %v", err)
	}

	if gotChatID != "12345" {
		t.Errorf("chat_id = %q, want %q", gotChatID, "12345")
	}
	if gotFilename != "report.pdf" {
		t.Errorf("filename = %q, want %q", gotFilename, "report.pdf")
	}
	if string(gotContent) != "hello document" {
		t.Errorf("content = %q, want %q", string(gotContent), "hello document")
	}
	if gotCaption != "monthly report" {
		t.Errorf("caption = %q, want %q", gotCaption, "monthly report")
	}
	if gotContentType == "application/octet-stream" || gotContentType == "" {
		t.Errorf("content type = %q, want multipart form data", gotContentType)
	}
}

func TestUploadDocumentMultipartDefaults(t *testing.T) {
	var gotFilename string
	var gotChatID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotChatID = r.FormValue("chat_id")
		gotFilename = r.MultipartForm.File["document"][0].Filename
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()

	adapter := &Adapter{token: "test-token", apiBase: server.URL, httpClient: server.Client()}

	media := &plugin.MediaInfo{Type: "document", Data: []byte("raw")}
	if err := adapter.uploadDocument(context.Background(), -100200, media); err != nil {
		t.Fatalf("uploadDocument: %v", err)
	}

	if gotChatID != "-100200" {
		t.Errorf("chat_id = %q, want %q", gotChatID, "-100200")
	}
	if gotFilename != "document.bin" {
		t.Errorf("filename = %q, want default document.bin", gotFilename)
	}
}
