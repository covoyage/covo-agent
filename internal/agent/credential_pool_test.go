package agent

import (
	"testing"
	"time"
)

func TestCredentialPoolResetExhaustedPreservesDeadCredentials(t *testing.T) {
	pool := NewCredentialPool("test", StrategyFillFirst)
	pool.AddCredential(&Credential{ID: "ok", Status: CredentialStatusOK})
	pool.AddCredential(&Credential{ID: "exhausted", Status: CredentialStatusExhausted, ExhaustedAt: time.Now(), LastError: "rate limit"})
	pool.AddCredential(&Credential{ID: "dead", Status: CredentialStatusDead, LastError: "invalid"})

	if got := pool.ResetExhausted(); got != 1 {
		t.Fatalf("ResetExhausted() = %d, want 1", got)
	}
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	if credential := pool.credentials["exhausted"]; credential.Status != CredentialStatusOK || !credential.ExhaustedAt.IsZero() || credential.LastError != "" {
		t.Fatalf("exhausted credential not reset: %+v", credential)
	}
	if credential := pool.credentials["dead"]; credential.Status != CredentialStatusDead || credential.LastError != "invalid" {
		t.Fatalf("dead credential changed: %+v", credential)
	}
}
