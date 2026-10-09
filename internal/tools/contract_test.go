package tools

import "testing"

func TestLookupContractReadOnly(t *testing.T) {
	c, ok := LookupContract("read")
	if !ok || !c.ReadOnly || !c.ConcurrentSafe {
		t.Fatalf("read contract = %+v ok=%v", c, ok)
	}
	if ContractReadOnly("bash") {
		t.Fatal("bash must not be read-only")
	}
	c, ok = LookupContract("bash")
	if !ok || c.MaxOutputBytes != 32_000 || c.SideEffectScope != ScopeSystem {
		t.Fatalf("bash contract = %+v", c)
	}
}

func TestLookupContractUnknown(t *testing.T) {
	if _, ok := LookupContract("not_a_tool"); ok {
		t.Fatal("unknown tool should miss")
	}
}
