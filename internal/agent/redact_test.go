package agent

import (
	"strings"
	"testing"
)

func TestRedactSensitiveTextForceStructuredBodies(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		secrets []string
	}{
		{
			name:    "nested JSON",
			input:   `{"request":{"api_key":"json-secret"},"items":[{"password":"nested-secret"}],"safe":"visible"}`,
			secrets: []string{"json-secret", "nested-secret"},
		},
		{
			name:    "form",
			input:   `token=form-secret&safe=visible`,
			secrets: []string{"form-secret"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redacted := RedactSensitiveTextForce(test.input)
			for _, secret := range test.secrets {
				if strings.Contains(redacted, secret) {
					t.Fatalf("redacted text contains %q: %s", secret, redacted)
				}
			}
			if !strings.Contains(redacted, "visible") {
				t.Fatalf("redacted text removed safe value: %s", redacted)
			}
		})
	}
}

func TestRedactSensitiveTextForceURLUserinfo(t *testing.T) {
	redacted := RedactSensitiveTextForce("request https://user:url-secret@example.com/path")
	if strings.Contains(redacted, "url-secret") {
		t.Fatalf("redacted URL contains password: %s", redacted)
	}
	if !strings.Contains(redacted, "https://user:***@example.com/path") {
		t.Fatalf("redacted URL = %s", redacted)
	}
}
