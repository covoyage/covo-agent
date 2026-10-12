package cyber

import (
	"net/url"
	"testing"
)

func TestIntakeOperationShapes(t *testing.T) {
	jobFor := func(rawURL, request string) IntakeJob {
		t.Helper()
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		return intakeJobForExchange(Exchange{Method: "POST", URL: rawURL, Request: request, Identity: "alice"}, parsed)
	}
	first := jobFor("https://example.test/orders/123", `{"item":{"id":1}}`)
	second := jobFor("https://example.test/orders/456", `{"item":{"id":2}}`)
	if first.Key != second.Key {
		t.Fatal("resource values should share an operation shape")
	}
	if first.Endpoint == second.Endpoint {
		t.Fatal("grouping must not replace the actual replay endpoint")
	}
	extra := jobFor("https://example.test/orders/123", `{"item":{"id":1,"role":"admin"}}`)
	if first.Key == extra.Key {
		t.Fatal("nested field additions must retain a distinct shape")
	}
	query := jobFor("https://example.test/graphql", `{"query":"query User($id: ID!){user(id:$id){name}}","variables":{"id":1}}`)
	mutation := jobFor("https://example.test/graphql", `{"query":"mutation Update($id: ID!){updateUser(id:$id){name}}","variables":{"id":1}}`)
	if query.Key == mutation.Key {
		t.Fatal("distinct GraphQL operations must not be merged")
	}
	otherValue := jobFor("https://example.test/graphql", `{"query":"query User($id: ID!){user(id:$id){name}}","variables":{"id":2}}`)
	if query.Key != otherValue.Key {
		t.Fatal("GraphQL variable values must not change the operation shape")
	}
	encoded := jobFor("https://example.test/orders/%31%32%33", `{"item":{"id":1}}`)
	if first.Key == encoded.Key {
		t.Fatal("encoded path variants must not be normalized away")
	}
	if jobFor("https://example.test/api?action=list", "").Key == jobFor("https://example.test/api?action=delete", "").Key {
		t.Fatal("query-dispatched operations must remain distinct")
	}
	if jobFor("https://example.test/api", `{"action":"list","id":1}`).Key == jobFor("https://example.test/api", `{"action":"delete","id":1}`).Key {
		t.Fatal("body-dispatched operations must remain distinct")
	}
	if jobFor("https://example.test/api", "action=list&id=1").Key == jobFor("https://example.test/api", "action=delete&id=1").Key {
		t.Fatal("form-dispatched operations must remain distinct")
	}
	if jobFor("https://example.test/rpc", `[{"jsonrpc":"2.0","method":"getUser","params":{"id":1}}]`).Key == jobFor("https://example.test/rpc", `[{"jsonrpc":"2.0","method":"deleteUser","params":{"id":1}}]`).Key {
		t.Fatal("batch operations must fall back to exact data")
	}
	if jobFor("https://example.test/rpc", `{"jsonrpc":"2.0","method":"getUser","params":{"id":1},"id":1}`).Key == jobFor("https://example.test/rpc", `{"jsonrpc":"2.0","method":"deleteUser","params":{"id":1},"id":1}`).Key {
		t.Fatal("JSON-RPC methods must remain distinct")
	}
}

func TestIntakeScopeRevocationAndRecoveryLimit(t *testing.T) {
	workDir := t.TempDir()
	catalog := NewCatalog(workDir)
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	ex, err := catalog.RecordExchange(Exchange{URL: "https://example.test/orders/1", Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"other.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := catalog.ClaimIntake(); err != nil || ok {
		t.Fatalf("revoked host dispatched: ok=%v err=%v", ok, err)
	}
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	for count := 0; count < 3; count++ {
		if _, ok, err := catalog.ClaimIntake(); err != nil || !ok {
			t.Fatalf("claim %d: ok=%v err=%v", count, ok, err)
		}
		if err := catalog.RecoverIntake(); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := catalog.ClaimIntake(); err != nil || ok {
		t.Fatalf("crash recovery bypassed review limit: ok=%v err=%v", ok, err)
	}
	context, err := NewCatalog(workDir).RequestContext(ex.ID)
	if err != nil || len(context.Observations) != 1 {
		t.Fatalf("durable context missing: %+v err=%v", context, err)
	}
}

func TestIntakeIncompleteRequestsAreNotMerged(t *testing.T) {
	for _, incomplete := range []Exchange{
		{RequestTruncated: true}, {HeadersTruncated: true}, {Redacted: true}, {Request: "unparsed request bytes"},
	} {
		incomplete.Method, incomplete.URL = "GET", "https://example.test/orders/1"
		firstURL, _ := url.Parse(incomplete.URL)
		first := intakeJobForExchange(incomplete, firstURL)
		incomplete.URL = "https://example.test/orders/2"
		secondURL, _ := url.Parse(incomplete.URL)
		if first.Key == intakeJobForExchange(incomplete, secondURL).Key {
			t.Fatalf("incomplete requests were merged: %+v", incomplete)
		}
	}
}

func TestIntakeObservationPrecisionAndCapacity(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	ex, err := catalog.RecordExchange(Exchange{Method: "POST", URL: "https://example.test/orders", Request: `{"id":9007199254740993}`})
	if err != nil {
		t.Fatal(err)
	}
	context, err := catalog.RequestContext(ex.ID)
	if err != nil || context.Observations[0].Parameters[0].Value != "9007199254740993" {
		t.Fatalf("large ID lost precision: %+v err=%v", context, err)
	}
	items := make([]RequestObservation, 500)
	for index := range items {
		items[index] = RequestObservation{OperationKey: "unrelated", ExchangeID: "old", Identity: "old"}
	}
	if err := catalog.writeJSON(catalog.requestObservationsPath(), items); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(Exchange{URL: "https://example.test/new"}); err == nil {
		t.Fatal("observation capacity must fail explicitly")
	}
}

func TestIntakeRelabelCompletedIdentityDoesNotReuseOldTask(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	ex, err := catalog.RecordExchange(Exchange{URL: "https://example.test/orders/1", Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := catalog.ClaimIntake()
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.CompleteIntake(job.Key, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.LabelExchange(ex.ID, Exchange{Identity: "bob"}); err != nil {
		t.Fatal(err)
	}
	job, ok, err := catalog.ClaimIntake()
	if err != nil || !ok || job.Identity != "bob" {
		t.Fatalf("stale identity dispatched: %+v ok=%v err=%v", job, ok, err)
	}
}

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

func TestIntakeObservationsAndIncrementalReview(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	first, err := catalog.RecordExchange(Exchange{URL: "https://example.test/orders/1?token=123456&owner=1", Identity: "alice", Request: `{"id":1,"password":123456,"csrf":123456,"note":"private text"}`})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := catalog.ClaimIntake()
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if err := catalog.CompleteIntake(claimed.Key, nil); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"bob", "bob"} {
		if _, err := catalog.RecordExchange(Exchange{URL: "https://example.test/orders/2?token=654321&owner=2", Identity: identity, Request: `{"id":2,"password":654321,"csrf":654321,"note":"other private text"}`}); err != nil {
			t.Fatal(err)
		}
	}
	context, err := catalog.RequestContext(first.ID)
	if err != nil || len(context.Observations) != 2 {
		t.Fatalf("context=%+v err=%v", context, err)
	}
	for _, observation := range context.Observations {
		for _, parameter := range observation.Parameters {
			if parameter.Name == "token" || parameter.Name == "/password" || parameter.Name == "/csrf" || parameter.Value == "private text" || parameter.Value == "other private text" {
				t.Fatalf("secret leaked: %+v", parameter)
			}
		}
	}
	jobs, err := catalog.IntakeJobs()
	if err != nil || len(jobs) != 2 || jobs[0].Status != "pending" || jobs[0].Endpoint != claimed.Endpoint {
		t.Fatalf("old operation should be requeued without changing its scope: %+v err=%v", jobs, err)
	}
	claimed, ok, err = catalog.ClaimIntake()
	if err != nil || !ok || claimed.ContextRevision != context.Revision || claimed.ReviewCount != 2 {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, err := catalog.LabelExchange(first.ID, Exchange{Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CompleteIntake(claimed.Key, nil); err != nil {
		t.Fatal(err)
	}
	jobs, _ = catalog.IntakeJobs()
	if jobs[0].Status != "pending" {
		t.Fatalf("context arriving during a run was lost: %+v", jobs)
	}
	claimed, _, err = catalog.ClaimIntake()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.LabelExchange(first.ID, Exchange{Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CompleteIntake(claimed.Key, nil); err != nil {
		t.Fatal(err)
	}
	jobs, _ = catalog.IntakeJobs()
	if jobs[0].Status != "blocked" || jobs[0].ReviewCount != 3 {
		t.Fatalf("automatic reviews should be bounded: %+v", jobs)
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
