package cyber

import "testing"

func TestThreatModelPersistsAndAmends(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	saved, err := catalog.SaveThreatModel(ThreatModel{
		Target: "https://Example.test:443/api/", Overview: "API", Attacker: "unauthenticated user",
		TrustBoundaries: []string{"browser to API"}, AttackSurface: []string{"/api"}, SeverityCalibration: "tenant escape is high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Identity != "example.test/api" {
		t.Fatalf("identity = %q", saved.Identity)
	}
	if _, err := catalog.AmendThreatModel("example.test/api", "verifier", "Admin route is reachable without VPN."); err != nil {
		t.Fatal(err)
	}
	reloaded, ok, err := NewCatalog(dir).GetThreatModel("https://example.test/api/")
	if err != nil || !ok || len(reloaded.Amendments) != 1 {
		t.Fatalf("reloaded=%#v ok=%v err=%v", reloaded, ok, err)
	}
}
