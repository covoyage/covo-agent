package agent

import "testing"

func TestSessionLeaseClaimAndStale(t *testing.T) {
	var l SessionLease
	gen, err := l.Claim("tui")
	if err != nil || gen == 0 {
		t.Fatalf("claim: %d %v", gen, err)
	}
	if err := l.begin(gen); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Claim("acp"); err == nil {
		t.Fatal("second owner while busy should fail")
	}
	l.end()
	if _, err := l.Claim("acp"); err != nil {
		t.Fatal(err)
	}
	if err := l.begin(gen); err == nil {
		t.Fatal("stale generation should fail")
	}
}
