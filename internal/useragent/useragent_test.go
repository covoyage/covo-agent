package useragent

import "testing"

func TestUserAgent_EnvOverride(t *testing.T) {
	t.Setenv("COVO_USER_AGENT", "custom-agent/9.9")
	if got := UserAgent("covo-agent/0.1.0-dev"); got != "custom-agent/9.9" {
		t.Errorf("UserAgent() = %q, want override %q", got, "custom-agent/9.9")
	}
}

func TestUserAgent_Fallback(t *testing.T) {
	t.Setenv("COVO_USER_AGENT", "")
	if got := UserAgent("covo-agent/0.1.0-dev"); got != "covo-agent/0.1.0-dev" {
		t.Errorf("UserAgent() = %q, want fallback %q", got, "covo-agent/0.1.0-dev")
	}
}
