package cyber

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestExchangeCompletenessSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	first, err := NewCatalog(dir).RecordExchange(Exchange{
		URL: "https://example.test/submit", Method: "POST",
		ReqHeaders: strings.Repeat("a", 2100), Request: strings.Repeat("b", 4100), Response: strings.Repeat("c", 8100),
	})
	if err != nil {
		t.Fatal(err)
	}
	ex, found, err := NewCatalog(dir).GetExchange(first.ID)
	if err != nil || !found || !ex.HeadersTruncated || !ex.RequestTruncated || !ex.ResponseTruncated {
		t.Fatalf("truncation metadata lost: %+v, found=%v, %v", ex, found, err)
	}
	ex, err = NewCatalog(dir).RecordExchange(Exchange{URL: "https://example.test/submit", Method: "POST", Request: "  exact bytes\n"})
	if err != nil || ex.Request != "  exact bytes\n" || ex.RequestTruncated {
		t.Fatalf("complete request bytes changed: %+v, %v", ex, err)
	}
}

func TestExchangeRetentionReleasesFinishedJobs(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			catalog := NewCatalog(t.TempDir())
			items := make([]Exchange, 500)
			for index := range items {
				items[index] = Exchange{ID: fmt.Sprintf("ex-%d", index+1), URL: "https://example.test/"}
			}
			if err := catalog.writeJSON(catalog.exchangesPath(), items); err != nil {
				t.Fatal(err)
			}
			if err := catalog.writeJSON(catalog.intakeJobsPath(), []IntakeJob{{ExchangeID: "ex-1", Status: status}}); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.RecordExchange(Exchange{URL: items[0].URL}); err != nil {
				t.Fatal(err)
			}
			if _, found, err := catalog.GetExchange("ex-1"); err != nil || found {
				t.Fatalf("finished job still pins evidence: found=%v err=%v", found, err)
			}
		})
	}
}

func TestExchangeRetentionPreservesRequestObservation(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	items := make([]Exchange, 500)
	for index := range items {
		items[index] = Exchange{ID: fmt.Sprintf("ex-%d", index+1), URL: "https://example.test/"}
	}
	if err := catalog.writeJSON(catalog.exchangesPath(), items); err != nil {
		t.Fatal(err)
	}
	if err := catalog.writeJSON(catalog.requestObservationsPath(), []RequestObservation{{ExchangeID: "ex-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: items[0].URL}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := catalog.GetExchange("ex-1"); err != nil || !found {
		t.Fatalf("observation evidence was evicted: found=%v err=%v", found, err)
	}
}

func TestExchangeRetentionPreservesCoverageEvidence(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	items := make([]Exchange, 500)
	for index := range items {
		items[index] = Exchange{ID: fmt.Sprintf("ex-%d", index+1), URL: "https://example.test/"}
	}
	items[1].BaselineID = "ex-1"
	if err := catalog.writeJSON(catalog.exchangesPath(), items); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertCoverage(Coverage{Surface: "/", Outcome: "clean", ExchangeIDs: []string{"ex-2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: items[0].URL}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ex-1", "ex-2"} {
		if _, found, err := catalog.GetExchange(id); err != nil || !found {
			t.Fatalf("coverage evidence evicted: %s found=%v err=%v", id, found, err)
		}
	}
}

func TestExchangeRetentionConcurrentFindingWrites(t *testing.T) {
	workDir := t.TempDir()
	catalog := NewCatalog(workDir)
	items := make([]Exchange, 500)
	for index := range items {
		items[index] = Exchange{ID: fmt.Sprintf("ex-%d", index+1), Method: "GET", URL: "https://example.test/"}
	}
	if err := catalog.writeJSON(catalog.exchangesPath(), items); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		<-start
		for index := 0; index < 30; index++ {
			if _, err := NewCatalog(workDir).RecordExchange(Exchange{URL: items[0].URL}); err != nil {
				t.Errorf("record: %v", err)
				return
			}
		}
	}()
	go func() {
		defer writers.Done()
		<-start
		for index := 0; index < 30; index++ {
			_, err := NewCatalog(workDir).UpsertFinding(Finding{Title: fmt.Sprintf("finding %d", index), Target: items[0].URL, ExchangeID: items[index].ID})
			if err != nil && !strings.Contains(err.Error(), "not found") {
				t.Errorf("upsert: %v", err)
			}
		}
	}()
	close(start)
	writers.Wait()
	findings, err := catalog.ListFindings()
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if _, found, err := catalog.GetExchange(finding.ExchangeID); err != nil || !found {
			t.Fatalf("saved finding has missing evidence %s: %v", finding.ExchangeID, err)
		}
	}
}

func TestExchangeRetentionPreservesReferencedEvidence(t *testing.T) {
	for _, source := range []string{"finding", "pending", "running", "recent", "new"} {
		t.Run(source, func(t *testing.T) {
			workDir := t.TempDir()
			catalog := NewCatalog(workDir)
			items := make([]Exchange, 500)
			for index := range items {
				items[index] = Exchange{ID: fmt.Sprintf("ex-%d", index+1), Method: "GET", URL: "https://example.test/orders/1"}
			}
			items[1].BaselineID = items[0].ID
			referencedID := items[1].ID
			if source == "recent" {
				items[499].BaselineID = referencedID
			} else if source == "finding" {
				if err := catalog.writeJSON(catalog.findingsPath(), []Finding{{ExchangeID: referencedID}}); err != nil {
					t.Fatal(err)
				}
			} else if source != "new" {
				if err := catalog.writeJSON(catalog.intakeJobsPath(), []IntakeJob{{ExchangeID: referencedID, Status: source}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := catalog.writeJSON(catalog.exchangesPath(), items); err != nil {
				t.Fatal(err)
			}
			next := Exchange{URL: items[0].URL}
			if source == "new" {
				next.BaselineID = referencedID
			}
			added, err := catalog.RecordExchange(next)
			if err != nil || added.ID != "ex-501" {
				t.Fatalf("added=%+v err=%v", added, err)
			}
			reloaded := NewCatalog(workDir)
			retained, err := reloaded.ListExchanges()
			if err != nil || len(retained) != 500 {
				t.Fatalf("count=%d err=%v", len(retained), err)
			}
			for _, id := range []string{"ex-1", "ex-2", "ex-500", "ex-501"} {
				if _, found, err := reloaded.GetExchange(id); err != nil || !found {
					t.Fatalf("protected exchange %s missing: %v", id, err)
				}
			}
			if _, found, err := reloaded.GetExchange("ex-3"); err != nil || found {
				t.Fatalf("old unreferenced exchange retained: found=%v err=%v", found, err)
			}
		})
	}
}

func TestExchangeRetentionRejectsFullProtectedStorage(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	items := make([]Exchange, 500)
	findings := make([]Finding, 500)
	for index := range items {
		items[index] = Exchange{ID: fmt.Sprintf("ex-%d", index+1), URL: "https://example.test/"}
		findings[index].ExchangeID = items[index].ID
	}
	if err := catalog.writeJSON(catalog.exchangesPath(), items); err != nil {
		t.Fatal(err)
	}
	if err := catalog.writeJSON(catalog.findingsPath(), findings); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: items[0].URL}); err == nil {
		t.Fatal("full protected storage must reject a new record")
	}
	retained, err := catalog.ListExchanges()
	if err != nil || len(retained) != 500 || retained[0].ID != "ex-1" || retained[499].ID != "ex-500" {
		t.Fatalf("storage changed on rejection: count=%d err=%v", len(retained), err)
	}
}

func TestSearchExchangesIndexesPathAndParameters(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.RecordExchange(Exchange{
		Method: "post", URL: "https://api.example.test/users/42?expand=teams",
		StatusCode: 403, Request: `{"tenantId":"other","role":"admin"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(Exchange{Method: "GET", URL: "https://api.example.test/health", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}

	items, err := catalog.SearchExchanges(ExchangeFilter{Host: "example.test", Path: "/users", Parameter: "tenantid", StatusCode: 403})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Method != "POST" || !containsFold(items[0].Parameters, "expand") {
		t.Fatalf("unexpected search result: %#v", items)
	}
}

func TestExchangeIdentityContextRoundTrip(t *testing.T) {
	workDir := t.TempDir()
	catalog := NewCatalog(workDir)
	baseline, err := catalog.RecordExchange(Exchange{URL: "https://example.test/orders/1", Identity: "alice", Role: "customer", Tenant: "alpha", Resource: "order:1", Owner: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	compared, err := catalog.RecordExchange(Exchange{URL: baseline.URL, Identity: "bob", Role: "customer", Tenant: "beta", Resource: baseline.Resource, Owner: "alice", BaselineID: baseline.ID})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok, err := NewCatalog(workDir).GetExchange(compared.ID)
	if err != nil || !ok || reloaded.Identity != "bob" || reloaded.BaselineID != baseline.ID || reloaded.Owner != "alice" {
		t.Fatalf("reloaded=%+v ok=%v err=%v", reloaded, ok, err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: baseline.URL, BaselineID: "ex-missing"}); err == nil {
		t.Fatal("unknown baseline must be rejected")
	}
	if _, err := catalog.LabelExchange(compared.ID, Exchange{Identity: "reviewer", Tenant: "beta", Resource: "order:1", Owner: "alice", BaselineID: baseline.ID}); err != nil {
		t.Fatal(err)
	}
	results, err := catalog.SearchExchanges(ExchangeFilter{Identity: "reviewer", Tenant: "beta", Resource: "order:1"})
	if err != nil || len(results) != 1 || results[0].BaselineID != baseline.ID {
		t.Fatalf("identity search=%+v err=%v", results, err)
	}
}

func TestReplayBudgetCapsDailyRequests(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	for count := 0; count < 200; count++ {
		if err := catalog.ConsumeReplayBudget(); err != nil {
			t.Fatalf("budget rejected request %d: %v", count, err)
		}
	}
	if err := catalog.ConsumeReplayBudget(); err == nil {
		t.Fatal("daily budget should reject request 201")
	}
}
