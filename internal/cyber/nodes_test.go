package cyber

import (
	"context"
	"testing"
)

func TestRemoteNodeRegistry(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.UpsertNode(RemoteNode{ID: "--bad", Host: "host", User: "tester"}); err == nil {
		t.Fatal("unsafe node id accepted")
	}
	if _, err := catalog.UpsertNode(RemoteNode{ID: "edge", Host: "host", User: "tester", KeyPath: "-oProxyCommand=bad"}); err == nil {
		t.Fatal("option-like key path accepted")
	}
	if _, err := catalog.UpsertNode(RemoteNode{ID: "zone-a", Host: "host.example", User: "tester", Zone: "internal"}); err != nil {
		t.Fatal(err)
	}
	nodes, err := catalog.ListNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Zone != "internal" {
		t.Fatalf("nodes=%+v err=%v", nodes, err)
	}
	if _, err := catalog.ExecuteNode(context.Background(), "unknown", "hostname"); err == nil {
		t.Fatal("unregistered node was executed")
	}
	if err := catalog.RemoveNode("zone-a"); err != nil {
		t.Fatal(err)
	}
}
