package cyber

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

func semanticFindingID(candidate Finding, existing []Finding) string {
	if !hasSemanticAnchor(candidate) {
		return ""
	}
	for _, item := range existing {
		if sameFindingRootCause(candidate, item) {
			return item.ID
		}
	}
	return ""
}

func hasSemanticAnchor(f Finding) bool {
	return strings.TrimSpace(f.Endpoint) != "" || strings.TrimSpace(f.File) != "" ||
		strings.TrimSpace(f.Parameter) != "" || strings.TrimSpace(f.CVE) != "" ||
		strings.TrimSpace(f.RootCause) != ""
}

func sameFindingRootCause(a, b Finding) bool {
	if CanonicalTarget(a.Target) != CanonicalTarget(b.Target) {
		return false
	}
	if normalizeIdentity(a.Endpoint) != normalizeIdentity(b.Endpoint) ||
		normalizeIdentity(a.Method) != normalizeIdentity(b.Method) ||
		normalizeIdentity(a.Parameter) != normalizeIdentity(b.Parameter) ||
		normalizeIdentity(a.File) != normalizeIdentity(b.File) || a.Line != b.Line ||
		normalizeIdentity(a.CVE) != normalizeIdentity(b.CVE) {
		return false
	}
	rootA, rootB := normalizeIdentity(a.RootCause), normalizeIdentity(b.RootCause)
	return rootA != "" && rootA == rootB
}

func normalizeIdentity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func mergeFindingEvidence(old, next Finding) Finding {
	if next.Triage == "" {
		next.Triage, next.DuplicateOf = old.Triage, old.DuplicateOf
	}
	if next.SimilarTo == "" {
		next.SimilarTo = old.SimilarTo
	}
	if next.Evidence == "" {
		next.Evidence = old.Evidence
	} else if old.Evidence != "" && !strings.Contains(next.Evidence, old.Evidence) {
		next.Evidence = fmt.Sprintf("%s\n%s", old.Evidence, next.Evidence)
	}
	if next.PoC == "" {
		next.PoC = old.PoC
	}
	if next.ExchangeID == "" {
		next.ExchangeID = old.ExchangeID
	}
	return next
}

func generatedFindingID(f Finding) string {
	if !hasSemanticAnchor(f) {
		return slugID("f", f.Target, f.Title)
	}
	identity := strings.Join([]string{
		CanonicalTarget(f.Target), normalizeIdentity(f.Title), normalizeIdentity(f.Endpoint),
		normalizeIdentity(f.Method), normalizeIdentity(f.Parameter), normalizeIdentity(f.File),
		strconv.Itoa(f.Line), normalizeIdentity(f.CVE), normalizeIdentity(f.RootCause),
	}, "|")
	sum := sha256.Sum256([]byte(identity))
	return "f-" + hex.EncodeToString(sum[:8])
}
