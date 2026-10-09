package cyber

import "testing"

func TestFindingUpdateValidatesInheritedEvidence(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	ex, err := catalog.RecordExchange(Exchange{URL: "https://example.test/a", Method: "GET", Redacted: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalog.UpsertFinding(Finding{Title: "first", Target: "example.test", Endpoint: "/a", Method: "GET", ExchangeID: ex.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []Finding{
		{ID: first.ID, Title: "updated", Target: "other.test", PoC: "proof"},
		{ID: first.ID, Title: "updated", Target: "example.test", Endpoint: "/b", PoC: "proof"},
		{ID: first.ID, Title: "updated", Target: "example.test", Method: "POST", PoC: "proof"},
		{ID: first.ID, Title: "updated", Target: "example.test", Status: StatusConfirmed},
	} {
		if _, err := catalog.UpsertFinding(update); err == nil {
			t.Fatalf("invalid inherited evidence accepted: %+v", update)
		}
	}
	updated, err := catalog.UpsertFinding(Finding{ID: first.ID, Title: "updated", Target: "example.test", Status: StatusConfirmed, PoC: "independent proof"})
	if err != nil || updated.ExchangeID != ex.ID || updated.Endpoint != "/a" || updated.Method != "GET" {
		t.Fatalf("valid inherited evidence rejected: %+v, %v", updated, err)
	}
}

func TestSemanticFindingSuggestsTriageWithoutDroppingEvidence(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	first, err := catalog.UpsertFinding(Finding{
		Title: "Tenant data leak", Target: "example.test", Severity: "high", Status: StatusProofGap,
		Endpoint: "/api/users", Method: "GET", Parameter: "id", RootCause: "missing object ownership check",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.UpsertFinding(Finding{
		Title: "IDOR on users endpoint", Target: "https://example.test", Severity: "high", Status: StatusProofGap,
		Endpoint: "/api/users", Method: "GET", Parameter: "id", RootCause: "missing object ownership check",
		Evidence: "second observation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.SimilarTo != first.ID || second.Triage != "pending" {
		t.Fatalf("similar finding not retained for triage: %+v first=%+v", second, first)
	}
	items, _ := catalog.ListFindings()
	if len(items) != 2 {
		t.Fatalf("findings = %d, want 2", len(items))
	}
	if _, err := catalog.TriageFinding(second.ID, "duplicate", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.TriageFinding(first.ID, "duplicate", second.ID); err == nil {
		t.Fatal("duplicate chain should not be allowed")
	}
	approved, err := catalog.TriageFinding(second.ID, "approve", "")
	if err != nil || approved.DuplicateOf != "" || approved.Triage != "approved" {
		t.Fatalf("approval failed: %+v err=%v", approved, err)
	}
}

func TestSemanticFindingKeepsDifferentEndpoints(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	for _, endpoint := range []string{"/api/users", "/api/admins"} {
		if _, err := catalog.UpsertFinding(Finding{
			Title: "missing authorization", Target: "example.test", Severity: "high", Status: StatusProofGap,
			Endpoint: endpoint, Method: "GET", Parameter: "id", RootCause: "missing object ownership check",
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := catalog.ListFindings()
	if len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("distinct endpoints were merged: %#v", items)
	}
}
