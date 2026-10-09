package cyber

import "testing"

func TestEngagementRuntimeResumesPersistedState(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	id := EngagementID("/tmp/repo")
	first, err := catalog.EnsureRuntime("docker", id, "/tmp/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpdateRuntimeStatus("interrupted"); err != nil {
		t.Fatal(err)
	}
	resumed, err := catalog.EnsureRuntime("docker", id, "/tmp/repo")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.StartedAt != first.StartedAt || resumed.PreviousStop != "interrupted" || resumed.Status != "active" {
		t.Fatalf("unexpected resumed runtime: %#v", resumed)
	}
}
