package skillhub

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Provenance records where an installed skill came from and which exact
// bytes were trusted at install time. Written as .provenance.json next to
// SKILL.md so audits can trace any installed skill back to its source.
type Provenance struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	SHA256      string `json:"sha256,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at"`
}

// provenanceFile is the sidecar filename stored inside each skill directory.
const provenanceFile = ".provenance.json"

// writeProvenance persists the provenance sidecar for an installed skill.
// Failures are not fatal: the skill itself is already installed and verified.
func writeProvenance(skillDir string, p Provenance) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(skillDir, provenanceFile), data, 0644)
}

// ReadProvenance loads the provenance sidecar for a skill directory. Returns
// ok=false when no sidecar exists (e.g. bundled or hand-written skills).
func ReadProvenance(skillDir string) (Provenance, bool) {
	data, err := os.ReadFile(filepath.Join(skillDir, provenanceFile))
	if err != nil {
		return Provenance{}, false
	}
	var p Provenance
	if err := json.Unmarshal(data, &p); err != nil {
		return Provenance{}, false
	}
	return p, true
}
