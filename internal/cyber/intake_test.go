package cyber

import "testing"

func TestIntakeScopeDedupAndRecovery(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true}); err == nil {
		t.Fatal("intake without scope must fail")
	}
	config, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"https://example.test", "example.test"}})
	if err != nil || len(config.Hosts) != 1 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	outside, err := catalog.RecordExchange(Exchange{URL: "https://other.test/api?a=1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, queued, err := catalog.QueueExchange(outside); err != nil || queued {
		t.Fatalf("out of scope queued=%v err=%v", queued, err)
	}
	first, err := catalog.RecordExchange(Exchange{Method: "POST", URL: "https://example.test/api?a=1", Request: `{"id":1}`, Identity: "test-user"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := catalog.IntakeJobs()
	if err != nil || len(jobs) != 1 || jobs[0].ExchangeID != first.ID {
		t.Fatalf("first request not queued automatically: %+v err=%v", jobs, err)
	}
	job := jobs[0]
	second, err := catalog.RecordExchange(Exchange{Method: "POST", URL: "https://example.test/api?a=2", Request: `{"id":2}`, Identity: "test-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, queued, err := catalog.QueueExchange(second); err != nil || queued {
		t.Fatalf("duplicate queued=%v err=%v", queued, err)
	}
	claimed, ok, err := catalog.ClaimIntake()
	if err != nil || !ok || claimed.Key != job.Key {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err := catalog.ClaimIntake(); err != nil || ok {
		t.Fatalf("double claim ok=%v err=%v", ok, err)
	}
	if err := catalog.RecoverIntake(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := catalog.ClaimIntake(); err != nil || !ok {
		t.Fatalf("recovery claim ok=%v err=%v", ok, err)
	}
}

func TestIntakeRejectsAmbiguousScopeAndReplayFeedback(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"https://example.test/private"}}); err == nil {
		t.Fatal("URL path must not silently expand to a whole-host scope")
	}
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	baseline, err := catalog.RecordExchange(Exchange{URL: "https://example.test/a", Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: "https://example.test/a", Identity: "bob", BaselineID: baseline.ID}); err != nil {
		t.Fatal(err)
	}
	jobs, err := catalog.IntakeJobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("replay should not enqueue: jobs=%+v err=%v", jobs, err)
	}
}

func TestIntakeDistinguishesPorts(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{"https://example.test:8443/a", "https://example.test:9443/a"} {
		if _, err := catalog.RecordExchange(Exchange{URL: rawURL}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := catalog.IntakeJobs()
	if err != nil || len(jobs) != 2 || jobs[0].Key == jobs[1].Key {
		t.Fatalf("distinct origins should have separate jobs: %+v err=%v", jobs, err)
	}
}

func TestIntakeRelabelPendingIdentity(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	ex, err := catalog.RecordExchange(Exchange{URL: "https://example.test/a", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.LabelExchange(ex.ID, Exchange{Identity: "alice"}); err != nil {
		t.Fatal(err)
	}
	jobs, err := catalog.IntakeJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Identity != "alice" {
		t.Fatalf("stale intake identity: %+v err=%v", jobs, err)
	}
	got, _, err := catalog.GetExchange(ex.ID)
	if err != nil || got.Role != "member" || got.Identity != "alice" {
		t.Fatalf("omitted role was cleared: %+v err=%v", got, err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: ex.URL, Identity: "alice"}); err != nil {
		t.Fatal(err)
	}
	jobs, err = catalog.IntakeJobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("duplicate job after relabel: %+v err=%v", jobs, err)
	}
}
