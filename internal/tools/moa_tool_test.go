package tools

import (
	"testing"
)

func TestMoaDefaultModelListUsesPrimary(t *testing.T) {
	t.Setenv("COVO_MOA_MODELS", "")
	got := moaDefaultModelList("qwen-plus")
	if len(got) != 1 || got[0] != "qwen-plus" {
		t.Fatalf("got %#v", got)
	}
}

func TestMoaDefaultModelListUsesEnv(t *testing.T) {
	t.Setenv("COVO_MOA_MODELS", "a, b , ,c")
	got := moaDefaultModelList("ignored")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %#v", got)
	}
}

func TestSplitModelListEmpty(t *testing.T) {
	if got := splitModelList(""); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
