package cyber

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOASTExchangeReferenceSurvivesRetention(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	source, err := catalog.RecordExchange(Exchange{URL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.BeginOAST("https://oast.test", "callback.test", "source", "", source.ID, []*http.Cookie{{Name: "session", Value: "secret"}}); err != nil {
		t.Fatal(err)
	}
	items := []Exchange{source}
	for index := 0; index < 500; index++ {
		items = append(items, Exchange{ID: fmt.Sprintf("ex-%d", index+2), URL: "https://example.test"})
	}
	retained, err := catalog.retainExchangesLocked(items, "ex-501")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, exchange := range retained {
		found = found || exchange.ID == source.ID
	}
	if !found || len(retained) != 500 {
		t.Fatal("OAST source evidence evicted")
	}
}

func TestOASTSessionIsolationCorrelationAndExpiry(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	finding, err := catalog.UpsertFinding(Finding{Title: "candidate", Target: "example.test", Evidence: "original"})
	if err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: "session", Value: "private-cookie"}}
	first, err := catalog.BeginOAST("https://oast.test", "callback.test", "source", finding.ID, "", cookies)
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.BeginOAST("https://oast.test", "callback.test", "source", finding.ID, "", cookies)
	if err != nil || first.Domain == second.Domain {
		t.Fatal(second, err)
	}
	observation := OASTObservation{Name: first.Domain, RemoteAddress: "192.0.2.1", ProviderTime: "provider-time"}
	recorded, err := catalog.RecordOAST(first.ID, []OASTObservation{observation, {Name: second.Domain}, {Name: "suffix" + first.Domain}})
	if err != nil || len(recorded.Observations) != 1 {
		t.Fatal(recorded, err)
	}
	recorded, err = catalog.RecordOAST(first.ID, []OASTObservation{observation})
	if err != nil || len(recorded.Observations) != 1 {
		t.Fatal("duplicate observation", err)
	}
	list, err := catalog.ListOAST()
	data, _ := json.Marshal(list)
	if err != nil || strings.Contains(string(data), "private-cookie") {
		t.Fatal("credential leaked", err)
	}
	findings, _ := catalog.ListFindings()
	if findings[0].Evidence != "original" || findings[0].Status == "confirmed" {
		t.Fatal("callback changed finding")
	}
	if _, _, err := catalog.OASTCredentials(first.ID, "https://other.test"); err == nil {
		t.Fatal("provider changed")
	}
	var states []oastState
	if err := catalog.readJSON(catalog.oastPath(), &states); err != nil {
		t.Fatal(err)
	}
	states[0].ExpiresAt = time.Now().Add(-time.Second)
	if err := catalog.writeJSON(catalog.oastPath(), states); err != nil {
		t.Fatal(err)
	}
	if _, _, err := catalog.OASTCredentials(first.ID, first.Provider); err == nil {
		t.Fatal("expired credentials returned")
	}
	if _, err := catalog.RecordOAST(first.ID, []OASTObservation{observation}); err == nil {
		t.Fatal("expired observation recorded")
	}
}
