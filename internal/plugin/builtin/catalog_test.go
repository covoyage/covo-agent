package builtin

import (
	"reflect"
	"sort"
	"testing"
)

func TestCatalogEntriesAreCompleteAndConsistent(t *testing.T) {
	entries := Entries()
	if len(entries) == 0 {
		t.Fatal("built-in platform catalog is empty")
	}

	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			t.Fatal("catalog entry has an empty ID")
		}
		if entry.Description == "" {
			t.Errorf("platform %q has no description", entry.ID)
		}
		if entry.New == nil {
			t.Errorf("platform %q has no constructor", entry.ID)
			continue
		}
		if _, exists := seen[entry.ID]; exists {
			t.Errorf("duplicate platform ID %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}

		provider := entry.New()
		named, ok := provider.(interface{ GetID() string })
		if !ok {
			t.Errorf("platform %q provider does not expose GetID", entry.ID)
			continue
		}
		if got := named.GetID(); got != entry.ID {
			t.Errorf("platform catalog ID %q does not match provider ID %q", entry.ID, got)
		}
	}
}

func TestNamesAndDescriptionsComeFromCatalog(t *testing.T) {
	entries := Entries()
	wantNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		wantNames = append(wantNames, entry.ID)
		if got := Description(entry.ID); got != entry.Description {
			t.Errorf("Description(%q) = %q, want %q", entry.ID, got, entry.Description)
		}
	}
	sort.Strings(wantNames)

	if got := Names(); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("Names() = %v, want %v", got, wantNames)
	}
	if got := Description("missing-platform"); got != "" {
		t.Fatalf("Description(missing-platform) = %q, want empty", got)
	}
}

func TestProvidersReturnsFreshInstances(t *testing.T) {
	first := Providers()
	second := Providers()
	if len(first) != len(second) || len(first) != len(Entries()) {
		t.Fatalf("provider count mismatch: first=%d second=%d entries=%d", len(first), len(second), len(Entries()))
	}
	for i := range first {
		if reflect.ValueOf(first[i]).Pointer() == reflect.ValueOf(second[i]).Pointer() {
			t.Errorf("provider %q constructor reused an instance", Entries()[i].ID)
		}
	}
}
